package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// CreateTeamMessage persists one broadcast and a pending delivery row for every
// recipient in a single transaction. The message must survive before any send
// runs, so a crash mid-broadcast cannot lose the recipients that were attempted.
func (s *Store) CreateTeamMessage(
	ctx context.Context,
	message domain.TeamMessage,
	recipients []domain.SessionID,
) (domain.TeamMessageReport, error) {
	if err := message.Validate(); err != nil {
		return domain.TeamMessageReport{}, err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	err := s.inTx(ctx, "create team message", func(q *gen.Queries) error {
		if err := q.InsertTeamMessage(ctx, gen.InsertTeamMessageParams{
			ID:                string(message.ID),
			ProjectID:         string(message.ProjectID),
			TeamRootSessionID: string(message.TeamRootSessionID),
			SenderSessionID:   string(message.SenderSessionID),
			Body:              message.Body,
			CreatedAt:         message.CreatedAt,
		}); err != nil {
			return err
		}
		for _, recipient := range recipients {
			if err := q.InsertTeamMessageDelivery(ctx, gen.InsertTeamMessageDeliveryParams{
				MessageID: string(message.ID),
				SessionID: string(recipient),
				Status:    string(domain.TeamDeliveryPending),
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return domain.TeamMessageReport{}, fmt.Errorf("create team message %s: %w", message.ID, err)
	}
	deliveries := make([]domain.TeamMessageDelivery, 0, len(recipients))
	for _, recipient := range recipients {
		deliveries = append(deliveries, domain.TeamMessageDelivery{
			MessageID: message.ID,
			SessionID: recipient,
			Status:    domain.TeamDeliveryPending,
		})
	}
	return domain.TeamMessageReport{Message: message, Deliveries: deliveries}, nil
}

// SetTeamMessageDelivery records one member's final outcome. It reports false
// when the broadcast or the member row is missing.
func (s *Store) SetTeamMessageDelivery(
	ctx context.Context,
	messageID domain.TeamMessageID,
	sessionID domain.SessionID,
	status domain.TeamDeliveryStatus,
	deliveryError string,
) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.UpdateTeamMessageDelivery(ctx, gen.UpdateTeamMessageDeliveryParams{
		Status:    string(status),
		Error:     deliveryError,
		MessageID: string(messageID),
		SessionID: string(sessionID),
	})
	if err != nil {
		return false, fmt.Errorf("update team delivery %s/%s: %w", messageID, sessionID, err)
	}
	return rows > 0, nil
}

// GetTeamMessage returns one persisted broadcast with its deliveries in member
// id order.
func (s *Store) GetTeamMessage(ctx context.Context, id domain.TeamMessageID) (domain.TeamMessageReport, bool, error) {
	row, err := s.qr.GetTeamMessage(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.TeamMessageReport{}, false, nil
	}
	if err != nil {
		return domain.TeamMessageReport{}, false, fmt.Errorf("get team message %s: %w", id, err)
	}
	deliveries, err := s.qr.ListTeamMessageDeliveries(ctx, string(id))
	if err != nil {
		return domain.TeamMessageReport{}, false, fmt.Errorf("list team deliveries for %s: %w", id, err)
	}
	report := domain.TeamMessageReport{
		Message:    teamMessageFromGen(row),
		Deliveries: make([]domain.TeamMessageDelivery, 0, len(deliveries)),
	}
	for _, delivery := range deliveries {
		report.Deliveries = append(report.Deliveries, teamDeliveryFromGen(delivery))
	}
	return report, true, nil
}

func teamMessageFromGen(row gen.TeamMessage) domain.TeamMessage {
	return domain.TeamMessage{
		ID:                domain.TeamMessageID(row.ID),
		ProjectID:         domain.ProjectID(row.ProjectID),
		TeamRootSessionID: domain.SessionID(row.TeamRootSessionID),
		SenderSessionID:   domain.SessionID(row.SenderSessionID),
		Body:              row.Body,
		CreatedAt:         row.CreatedAt,
	}
}

func teamDeliveryFromGen(row gen.TeamMessageDelivery) domain.TeamMessageDelivery {
	return domain.TeamMessageDelivery{
		MessageID: domain.TeamMessageID(row.MessageID),
		SessionID: domain.SessionID(row.SessionID),
		Status:    domain.TeamDeliveryStatus(row.Status),
		Error:     row.Error,
	}
}
