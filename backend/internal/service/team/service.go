// Package team serves the derived team surface and broadcasts to team members.
// Membership itself is derived in service/scheduling; this package adds the one
// durable team artifact, the broadcast and its per-member delivery results.
//
// A broadcast is best-effort: one unreachable member never rolls back the
// message or another member's delivery. The message row is written before any
// send runs, so the recipients attempted are always recoverable.
package team

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/scheduling"
)

// maxRecordedDeliveryError bounds the stored failure text. A harness error can
// embed a full command line or transcript snippet; the delivery row only needs
// enough to explain the failure.
const maxRecordedDeliveryError = 1024

// DerivedTeams is the read-only derivation surface from service/scheduling.
type DerivedTeams interface {
	Team(ctx context.Context, rootID domain.SessionID) (domain.Team, error)
	Teams(ctx context.Context, projectID domain.ProjectID) ([]domain.Team, error)
}

// Store is the durable broadcast surface.
type Store interface {
	CreateTeamMessage(ctx context.Context, message domain.TeamMessage, recipients []domain.SessionID) (domain.TeamMessageReport, error)
	SetTeamMessageDelivery(ctx context.Context, messageID domain.TeamMessageID, sessionID domain.SessionID, status domain.TeamDeliveryStatus, deliveryError string) (bool, error)
}

// Sender delivers one message to one live session. The session service
// satisfies it; a fake stands in for it in tests.
type Sender interface {
	Send(ctx context.Context, id domain.SessionID, message string, attachment *ports.SpawnAttachment) error
}

// Deps wires the team service. Teams, Store, and Sender are required.
type Deps struct {
	Teams  DerivedTeams
	Store  Store
	Sender Sender
	Clock  func() time.Time
	NewID  func() string
}

// Service is the team read and broadcast surface. It is safe for concurrent
// use: broadcasts share no mutable state beyond their dependencies.
type Service struct {
	teams  DerivedTeams
	store  Store
	sender Sender
	clock  func() time.Time
	newID  func() string
}

// New builds a team service.
func New(d Deps) *Service {
	clock := d.Clock
	if clock == nil {
		clock = time.Now
	}
	newID := d.NewID
	if newID == nil {
		newID = uuid.NewString
	}
	return &Service{teams: d.Teams, store: d.Store, sender: d.Sender, clock: clock, newID: newID}
}

// Status returns the team rooted at rootID. A session with no orchestrator
// ancestor is not found: there is no team to report.
func (s *Service) Status(ctx context.Context, rootID domain.SessionID) (domain.Team, error) {
	if strings.TrimSpace(string(rootID)) == "" {
		return domain.Team{}, apierr.Invalid("INVALID_SESSION_ID", "Session id is required", nil)
	}
	team, err := s.teams.Team(ctx, rootID)
	if err != nil {
		if errors.Is(err, scheduling.ErrTeamNotFound) {
			return domain.Team{}, apierr.NotFound("TEAM_NOT_FOUND", "Unknown team root or the session is not an orchestrator")
		}
		return domain.Team{}, fmt.Errorf("read team %s: %w", rootID, err)
	}
	return team, nil
}

// List returns every team in a project in lead creation order.
func (s *Service) List(ctx context.Context, projectID domain.ProjectID) ([]domain.Team, error) {
	if strings.TrimSpace(string(projectID)) == "" {
		return nil, apierr.Invalid("INVALID_PROJECT_ID", "Project id is required", nil)
	}
	teams, err := s.teams.Teams(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("list teams for %s: %w", projectID, err)
	}
	return teams, nil
}

// Broadcast records a message for the team and attempts delivery to every
// non-terminated member except the sender. Per-member send failures are part of
// the returned report, not an error: the caller can still show which members
// received the message.
func (s *Service) Broadcast(
	ctx context.Context,
	rootID domain.SessionID,
	senderSessionID domain.SessionID,
	body string,
) (domain.TeamMessageReport, error) {
	if strings.TrimSpace(string(rootID)) == "" {
		return domain.TeamMessageReport{}, apierr.Invalid("INVALID_SESSION_ID", "Session id is required", nil)
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return domain.TeamMessageReport{}, apierr.Invalid("TEAM_MESSAGE_REQUIRED", "Message is required", nil)
	}
	if utf8.RuneCountInString(body) > domain.MaxTeamMessageBodyLength {
		return domain.TeamMessageReport{}, apierr.Invalid(
			"TEAM_MESSAGE_TOO_LONG",
			fmt.Sprintf("Message must be %d characters or fewer", domain.MaxTeamMessageBodyLength),
			nil,
		)
	}

	team, err := s.Status(ctx, rootID)
	if err != nil {
		return domain.TeamMessageReport{}, err
	}
	recipients := make([]domain.SessionID, 0, len(team.Members))
	for _, member := range team.Members {
		if member.SessionID == senderSessionID {
			continue
		}
		recipients = append(recipients, member.SessionID)
	}

	message := domain.TeamMessage{
		ID:                domain.TeamMessageID(s.newID()),
		ProjectID:         team.ProjectID,
		TeamRootSessionID: team.RootSessionID,
		SenderSessionID:   senderSessionID,
		Body:              body,
		CreatedAt:         s.clock().UTC(),
	}
	report, err := s.store.CreateTeamMessage(ctx, message, recipients)
	if err != nil {
		return domain.TeamMessageReport{}, fmt.Errorf("record team message for %s: %w", rootID, err)
	}

	deliveryIndex := make(map[domain.SessionID]int, len(report.Deliveries))
	for i, delivery := range report.Deliveries {
		deliveryIndex[delivery.SessionID] = i
	}
	var updateErr error
	for _, recipient := range recipients {
		status, deliveryError := domain.TeamDeliveryDelivered, ""
		if sendErr := s.sender.Send(ctx, recipient, body, nil); sendErr != nil {
			status, deliveryError = domain.TeamDeliveryFailed, recordedDeliveryError(sendErr)
		}
		if i, ok := deliveryIndex[recipient]; ok {
			report.Deliveries[i].Status = status
			report.Deliveries[i].Error = deliveryError
		}
		ok, err := s.store.SetTeamMessageDelivery(ctx, message.ID, recipient, status, deliveryError)
		if err != nil {
			if updateErr == nil {
				updateErr = err
			}
			continue
		}
		if !ok && updateErr == nil {
			updateErr = fmt.Errorf("record delivery for %s/%s: delivery row not found", message.ID, recipient)
		}
	}
	if updateErr != nil {
		return report, updateErr
	}
	return report, nil
}

// recordedDeliveryError keeps the stored failure bounded and single-line so a
// delivery row stays readable in a CLI table.
func recordedDeliveryError(err error) string {
	text := strings.Join(strings.Fields(err.Error()), " ")
	if utf8.RuneCountInString(text) <= maxRecordedDeliveryError {
		return text
	}
	runes := []rune(text)
	return string(runes[:maxRecordedDeliveryError-1]) + "…"
}
