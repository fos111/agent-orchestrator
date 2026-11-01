package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

func teamSession(t *testing.T, s *sqlite.Store, project string, kind domain.SessionKind) domain.SessionRecord {
	t.Helper()
	rec := sampleRecord(project)
	rec.Kind = kind
	created, err := s.CreateSession(context.Background(), rec)
	if err != nil {
		t.Fatalf("create %s session: %v", kind, err)
	}
	return created
}

func sampleTeamMessage(project string, root domain.SessionID, sender domain.SessionID) domain.TeamMessage {
	return domain.TeamMessage{
		ID:                domain.TeamMessageID("msg-" + string(root)),
		ProjectID:         domain.ProjectID(project),
		TeamRootSessionID: root,
		SenderSessionID:   sender,
		Body:              "Rebase on main before continuing.",
		CreatedAt:         time.Now().UTC().Truncate(time.Second),
	}
}

// A persisted broadcast starts with one pending delivery per recipient and
// records each final outcome independently.
func TestCreateTeamMessagePersistsPendingDeliveriesAndFinalOutcomes(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "team")
	root := teamSession(t, s, "team", domain.KindOrchestrator)
	first := teamSession(t, s, "team", domain.KindWorker)
	second := teamSession(t, s, "team", domain.KindWorker)

	message := sampleTeamMessage("team", root.ID, "")
	report, err := s.CreateTeamMessage(ctx, message, []domain.SessionID{first.ID, second.ID})
	if err != nil {
		t.Fatalf("create message: %v", err)
	}
	if len(report.Deliveries) != 2 || report.Deliveries[0].Status != domain.TeamDeliveryPending {
		t.Fatalf("created report = %+v, want two pending deliveries", report)
	}

	if ok, err := s.SetTeamMessageDelivery(ctx, message.ID, first.ID, domain.TeamDeliveryDelivered, ""); err != nil || !ok {
		t.Fatalf("record delivered: ok=%v err=%v", ok, err)
	}
	if ok, err := s.SetTeamMessageDelivery(ctx, message.ID, second.ID, domain.TeamDeliveryFailed, "runtime unavailable"); err != nil || !ok {
		t.Fatalf("record failed: ok=%v err=%v", ok, err)
	}

	got, found, err := s.GetTeamMessage(ctx, message.ID)
	if err != nil || !found {
		t.Fatalf("get message: found=%v err=%v", found, err)
	}
	if got.DeliveredCount() != 1 || got.FailedCount() != 1 {
		t.Fatalf("counts = delivered %d failed %d, want 1/1", got.DeliveredCount(), got.FailedCount())
	}
	if got.Message.Body != message.Body || got.Message.TeamRootSessionID != root.ID || !got.Message.CreatedAt.Equal(message.CreatedAt) {
		t.Fatalf("round-trip message = %+v, want %+v", got.Message, message)
	}
	for _, delivery := range got.Deliveries {
		switch delivery.SessionID {
		case first.ID:
			if delivery.Status != domain.TeamDeliveryDelivered || delivery.Error != "" {
				t.Fatalf("first delivery = %+v, want delivered with no error", delivery)
			}
		case second.ID:
			if delivery.Status != domain.TeamDeliveryFailed || delivery.Error != "runtime unavailable" {
				t.Fatalf("second delivery = %+v, want failed with its error", delivery)
			}
		default:
			t.Fatalf("unexpected delivery %+v", delivery)
		}
	}
}

// A broadcast to a team with no other member is still recorded; the message row
// survives with zero deliveries.
func TestCreateTeamMessageAllowsNoRecipients(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "team")
	root := teamSession(t, s, "team", domain.KindOrchestrator)

	message := sampleTeamMessage("team", root.ID, root.ID)
	if _, err := s.CreateTeamMessage(ctx, message, nil); err != nil {
		t.Fatalf("create message: %v", err)
	}
	got, found, err := s.GetTeamMessage(ctx, message.ID)
	if err != nil || !found || len(got.Deliveries) != 0 {
		t.Fatalf("get message = %+v found=%v err=%v, want zero deliveries", got, found, err)
	}
}

// A rejected recipient set must not leave a half-recorded broadcast behind.
func TestCreateTeamMessageRollsBackOnDuplicateRecipient(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "team")
	root := teamSession(t, s, "team", domain.KindOrchestrator)
	worker := teamSession(t, s, "team", domain.KindWorker)

	message := sampleTeamMessage("team", root.ID, "")
	if _, err := s.CreateTeamMessage(ctx, message, []domain.SessionID{worker.ID, worker.ID}); err == nil {
		t.Fatal("duplicate recipient was accepted")
	}
	if _, found, err := s.GetTeamMessage(ctx, message.ID); err != nil || found {
		t.Fatalf("message survived a rejected create: found=%v err=%v", found, err)
	}
}

// The domain validates a broadcast before it reaches SQLite.
func TestCreateTeamMessageRejectsInvalidMessage(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "team")
	root := teamSession(t, s, "team", domain.KindOrchestrator)

	message := sampleTeamMessage("team", root.ID, "")
	message.Body = ""
	if _, err := s.CreateTeamMessage(ctx, message, nil); err == nil {
		t.Fatal("empty body was accepted")
	}
}

// Recording an outcome for a missing member reports not-found instead of
// silently succeeding.
func TestSetTeamMessageDeliveryReportsMissingMember(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "team")
	root := teamSession(t, s, "team", domain.KindOrchestrator)
	message := sampleTeamMessage("team", root.ID, "")
	if _, err := s.CreateTeamMessage(ctx, message, nil); err != nil {
		t.Fatalf("create message: %v", err)
	}
	ok, err := s.SetTeamMessageDelivery(ctx, message.ID, domain.SessionID("missing"), domain.TeamDeliveryDelivered, "")
	if err != nil || ok {
		t.Fatalf("missing delivery: ok=%v err=%v, want false/nil", ok, err)
	}
}
