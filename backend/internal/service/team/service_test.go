package team

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/scheduling"
)

type fakeTeams struct {
	team    domain.Team
	err     error
	teams   []domain.Team
	listErr error
}

func (f *fakeTeams) Team(context.Context, domain.SessionID) (domain.Team, error) {
	return f.team, f.err
}

func (f *fakeTeams) Teams(context.Context, domain.ProjectID) ([]domain.Team, error) {
	return f.teams, f.listErr
}

type fakeStore struct {
	created    []domain.TeamMessage
	recipients [][]domain.SessionID
	createErr  error
	updates    []fakeDeliveryUpdate
	updateErr  error
}

type fakeDeliveryUpdate struct {
	messageID domain.TeamMessageID
	sessionID domain.SessionID
	status    domain.TeamDeliveryStatus
	errText   string
}

func (f *fakeStore) CreateTeamMessage(
	_ context.Context,
	message domain.TeamMessage,
	recipients []domain.SessionID,
) (domain.TeamMessageReport, error) {
	if f.createErr != nil {
		return domain.TeamMessageReport{}, f.createErr
	}
	f.created = append(f.created, message)
	f.recipients = append(f.recipients, append([]domain.SessionID(nil), recipients...))
	deliveries := make([]domain.TeamMessageDelivery, 0, len(recipients))
	for _, recipient := range recipients {
		deliveries = append(deliveries, domain.TeamMessageDelivery{
			MessageID: message.ID, SessionID: recipient, Status: domain.TeamDeliveryPending,
		})
	}
	return domain.TeamMessageReport{Message: message, Deliveries: deliveries}, nil
}

func (f *fakeStore) SetTeamMessageDelivery(
	_ context.Context,
	messageID domain.TeamMessageID,
	sessionID domain.SessionID,
	status domain.TeamDeliveryStatus,
	deliveryError string,
) (bool, error) {
	if f.updateErr != nil {
		return false, f.updateErr
	}
	f.updates = append(f.updates, fakeDeliveryUpdate{
		messageID: messageID, sessionID: sessionID, status: status, errText: deliveryError,
	})
	return true, nil
}

type fakeSender struct {
	sent     []fakeSend
	failures map[domain.SessionID]error
}

type fakeSend struct {
	sessionID domain.SessionID
	message   string
}

func (f *fakeSender) Send(_ context.Context, id domain.SessionID, message string, attachment *ports.SpawnAttachment) error {
	f.sent = append(f.sent, fakeSend{sessionID: id, message: message})
	if attachment != nil {
		return errors.New("unexpected attachment")
	}
	return f.failures[id]
}

func sampleTeam() domain.Team {
	now := time.Now().UTC()
	lead := domain.TeamMember{SessionID: "ao-100", Kind: domain.KindOrchestrator, CreatedAt: now}
	first := domain.TeamMember{SessionID: "ao-101", ParentSessionID: "ao-100", Kind: domain.KindWorker, CreatedAt: now}
	second := domain.TeamMember{SessionID: "ao-102", ParentSessionID: "ao-100", Kind: domain.KindWorker, CreatedAt: now}
	return domain.Team{
		RootSessionID: "ao-100", ProjectID: "ao", Orchestrator: lead,
		Members: []domain.TeamMember{lead, first, second}, ActiveWorkers: 2, MaxActiveWorkers: 4,
	}
}

func newTestService(teams DerivedTeams, store Store, sender Sender) *Service {
	return New(Deps{
		Teams: teams, Store: store, Sender: sender,
		Clock: func() time.Time { return time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC) },
		NewID: func() string { return "msg-1" },
	})
}

// A broadcast reaches every non-terminated member except the sender and records
// each delivered outcome.
func TestBroadcastSendsToEveryMemberExceptSender(t *testing.T) {
	derived := &fakeTeams{team: sampleTeam()}
	store := &fakeStore{}
	sender := &fakeSender{}
	svc := newTestService(derived, store, sender)

	report, err := svc.Broadcast(context.Background(), "ao-100", "ao-101", "  Rebase on main.  ")
	if err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	if len(sender.sent) != 2 || sender.sent[0].sessionID != "ao-100" || sender.sent[1].sessionID != "ao-102" {
		t.Fatalf("sent = %+v, want the lead and the other worker", sender.sent)
	}
	if sender.sent[0].message != "Rebase on main." {
		t.Fatalf("sent body = %q, want it trimmed", sender.sent[0].message)
	}
	if len(store.created) != 1 || store.created[0].Body != "Rebase on main." ||
		store.created[0].ID != "msg-1" || store.created[0].SenderSessionID != "ao-101" ||
		store.created[0].TeamRootSessionID != "ao-100" || store.created[0].ProjectID != "ao" {
		t.Fatalf("recorded message = %+v", store.created)
	}
	if !store.created[0].CreatedAt.Equal(time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("created at = %v", store.created[0].CreatedAt)
	}
	if report.DeliveredCount() != 2 || report.FailedCount() != 0 || len(store.updates) != 2 {
		t.Fatalf("report = %+v updates=%+v", report, store.updates)
	}
}

// A send failure is a per-member result: the other members still receive the
// message and the caller gets no top-level error.
func TestBroadcastRecordsPerMemberFailureWithoutAborting(t *testing.T) {
	derived := &fakeTeams{team: sampleTeam()}
	store := &fakeStore{}
	sender := &fakeSender{failures: map[domain.SessionID]error{"ao-101": errors.New("runtime\nunavailable")}}
	svc := newTestService(derived, store, sender)

	report, err := svc.Broadcast(context.Background(), "ao-100", "", "Check the queue.")
	if err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	if len(sender.sent) != 3 {
		t.Fatalf("sent %d members, want all three when there is no sender", len(sender.sent))
	}
	if report.DeliveredCount() != 2 || report.FailedCount() != 1 {
		t.Fatalf("counts = %d/%d, want 2 delivered and 1 failed", report.DeliveredCount(), report.FailedCount())
	}
	var failed domain.TeamMessageDelivery
	for _, delivery := range report.Deliveries {
		if delivery.SessionID == "ao-101" {
			failed = delivery
		}
	}
	if failed.Status != domain.TeamDeliveryFailed || failed.Error != "runtime unavailable" {
		t.Fatalf("failed delivery = %+v", failed)
	}
	if len(store.updates) != 3 {
		t.Fatalf("recorded %d deliveries, want 3", len(store.updates))
	}
}

// A team with only its lead has no recipient; the broadcast is still recorded.
func TestBroadcastWithNoRecipientsPersistsMessageOnly(t *testing.T) {
	team := sampleTeam()
	team.Members = team.Members[:1]
	derived := &fakeTeams{team: team}
	store := &fakeStore{}
	sender := &fakeSender{}
	svc := newTestService(derived, store, sender)

	report, err := svc.Broadcast(context.Background(), "ao-100", "ao-100", "Anyone there?")
	if err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	if len(sender.sent) != 0 || len(report.Deliveries) != 0 || len(store.created) != 1 {
		t.Fatalf("sent=%+v report=%+v created=%+v", sender.sent, report, store.created)
	}
}

func TestBroadcastRejectsUnknownTeam(t *testing.T) {
	derived := &fakeTeams{err: scheduling.ErrTeamNotFound}
	store := &fakeStore{}
	sender := &fakeSender{}
	svc := newTestService(derived, store, sender)

	_, err := svc.Broadcast(context.Background(), "ao-999", "", "Hello")
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "TEAM_NOT_FOUND" {
		t.Fatalf("err = %v, want TEAM_NOT_FOUND", err)
	}
	if len(store.created) != 0 || len(sender.sent) != 0 {
		t.Fatal("unknown team caused a write or a send")
	}
}

func TestBroadcastRejectsEmptyAndOversizedBody(t *testing.T) {
	derived := &fakeTeams{team: sampleTeam()}
	store := &fakeStore{}
	sender := &fakeSender{}
	svc := newTestService(derived, store, sender)

	for name, body := range map[string]string{
		"empty":    "   ",
		"too long": strings.Repeat("x", domain.MaxTeamMessageBodyLength+1),
	} {
		_, err := svc.Broadcast(context.Background(), "ao-100", "", body)
		var apiErr *apierr.Error
		if !errors.As(err, &apiErr) || apiErr.Kind != apierr.KindInvalid {
			t.Fatalf("%s body err = %v, want a 400-class error", name, err)
		}
	}
	if len(store.created) != 0 || len(sender.sent) != 0 {
		t.Fatal("invalid body caused a write or a send")
	}
}

// The message is durable before the first send, so a recorder failure must stop
// the broadcast instead of delivering untracked messages.
func TestBroadcastStopsWhenMessageCannotBeRecorded(t *testing.T) {
	derived := &fakeTeams{team: sampleTeam()}
	store := &fakeStore{createErr: errors.New("disk full")}
	sender := &fakeSender{}
	svc := newTestService(derived, store, sender)

	if _, err := svc.Broadcast(context.Background(), "ao-100", "", "Hello"); err == nil {
		t.Fatal("broadcast succeeded without a durable message")
	}
	if len(sender.sent) != 0 {
		t.Fatalf("sent %d messages before recording", len(sender.sent))
	}
}

// Delivery update failures surface after the best-effort sends, with the report
// still describing what happened.
func TestBroadcastReturnsDeliveryRecordFailureWithReport(t *testing.T) {
	derived := &fakeTeams{team: sampleTeam()}
	store := &fakeStore{updateErr: errors.New("database locked")}
	sender := &fakeSender{}
	svc := newTestService(derived, store, sender)

	report, err := svc.Broadcast(context.Background(), "ao-100", "", "Hello")
	if err == nil {
		t.Fatal("delivery record failure was swallowed")
	}
	if len(report.Deliveries) != 3 || report.DeliveredCount() != 3 {
		t.Fatalf("report = %+v, want the three sends reported", report)
	}
}

func TestStatusMapsMissingTeamAndValidatesInput(t *testing.T) {
	svc := newTestService(&fakeTeams{err: scheduling.ErrTeamNotFound}, &fakeStore{}, &fakeSender{})
	var apiErr *apierr.Error
	if _, err := svc.Status(context.Background(), "ao-999"); !errors.As(err, &apiErr) || apiErr.Code != "TEAM_NOT_FOUND" {
		t.Fatalf("missing team err = %v", err)
	}
	apiErr = nil
	if _, err := svc.Status(context.Background(), " "); !errors.As(err, &apiErr) || apiErr.Code != "INVALID_SESSION_ID" {
		t.Fatalf("blank id err = %v", err)
	}
}

func TestListReturnsDerivedTeamsAndValidatesProject(t *testing.T) {
	derived := &fakeTeams{teams: []domain.Team{sampleTeam()}}
	svc := newTestService(derived, &fakeStore{}, &fakeSender{})
	teams, err := svc.List(context.Background(), "ao")
	if err != nil || len(teams) != 1 || teams[0].RootSessionID != "ao-100" {
		t.Fatalf("teams = %+v err=%v", teams, err)
	}
	var apiErr *apierr.Error
	if _, err := svc.List(context.Background(), ""); !errors.As(err, &apiErr) || apiErr.Code != "INVALID_PROJECT_ID" {
		t.Fatalf("blank project err = %v", err)
	}
}
