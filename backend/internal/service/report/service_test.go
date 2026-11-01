package report

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakeStore struct {
	session domain.SessionRecord
	ok      bool
	created domain.ReportRecord
	reports []domain.ReportRecord
}

func (f *fakeStore) GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error) {
	return f.session, f.ok, nil
}
func (f *fakeStore) CreateReport(_ context.Context, r domain.ReportRecord) (domain.ReportRecord, error) {
	f.created = r
	return r, nil
}
func (f *fakeStore) ListReportsByProject(context.Context, domain.ProjectID) ([]domain.ReportRecord, error) {
	return append([]domain.ReportRecord(nil), f.reports...), nil
}

func TestListProjectIsReadOnlyAndOrdered(t *testing.T) {
	at := time.Date(2026, 9, 15, 1, 2, 3, 0, time.UTC)
	outputs := []domain.ReportOutput{
		{Kind: domain.ReportOutputArtifact, Reference: "first"},
		{Kind: domain.ReportOutputArtifact, Reference: "second"},
	}
	st := &fakeStore{reports: []domain.ReportRecord{
		{ID: "c", CreatedAt: at.Add(time.Second)},
		{ID: "b", CreatedAt: at, Outputs: outputs},
		{ID: "a", CreatedAt: at},
	}}
	got, err := New(Deps{Store: st}).ListProject(context.Background(), "ao")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].ID != "a" || got[1].ID != "b" || got[2].ID != "c" {
		t.Fatalf("report order = %+v", got)
	}
	if len(got[1].Outputs) != 2 || got[1].Outputs[0].Reference != "first" || got[1].Outputs[1].Reference != "second" {
		t.Fatalf("output position order = %+v", got[1].Outputs)
	}
}

func TestCreateDerivesOwnershipAndFixedDeliveryDeadlines(t *testing.T) {
	now := time.Date(2026, 9, 7, 1, 2, 3, 0, time.UTC)
	tests := []struct {
		name      string
		input     CreateInput
		available time.Time
		settles   time.Time
	}{
		{name: "free form batches for one hour", input: CreateInput{Message: "status"}, available: now.Add(time.Hour)},
		{name: "output only batches for one hour", input: CreateInput{Outputs: []domain.ReportOutput{{Kind: domain.ReportOutputArtifact, Reference: "result"}}}, available: now.Add(time.Hour)},
		{name: "checkpoint batches for one hour", input: CreateInput{State: domain.ReportCheckpoint, Note: "checkpoint"}, available: now.Add(time.Hour)},
		{name: "needs input is immediate", input: CreateInput{State: domain.ReportNeedsInput, Note: "decision"}, available: now},
		{name: "stuck is immediate", input: CreateInput{State: domain.ReportStuck, Note: "blocked"}, available: now},
		{name: "done settles for five minutes", input: CreateInput{State: domain.ReportDone, Note: "finished"}, available: now.Add(5 * time.Minute), settles: now.Add(5 * time.Minute)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := &fakeStore{ok: true, session: domain.SessionRecord{ID: "ao-7", ProjectID: "ao", Kind: domain.KindWorker}}
			s := New(Deps{Store: st, Now: func() time.Time { return now }, NewID: func() string { return "rpt_1" }})
			tc.input.SessionID = "ao-7"
			r, err := s.Create(context.Background(), tc.input)
			if err != nil {
				t.Fatal(err)
			}
			if r.ID != "rpt_1" || r.ProjectID != "ao" || r.DeliveryState != domain.ReportPending || r.RepeatCount != 1 || !r.AvailableAt.Equal(tc.available) || !r.SettlementDeadline.Equal(tc.settles) {
				t.Fatalf("report=%+v", r)
			}
		})
	}
}

func TestCreateDefendsValidationAndOwnership(t *testing.T) {
	worker := &fakeStore{ok: true, session: domain.SessionRecord{ID: "ao-2", ProjectID: "ao", Kind: domain.KindWorker}}
	for _, tc := range []struct {
		name  string
		store *fakeStore
		input CreateInput
	}{
		{"missing session", worker, CreateInput{Message: "message"}},
		{"unknown session", &fakeStore{}, CreateInput{SessionID: "ao-2", State: domain.ReportDone, Note: "done"}},
		{"orchestrator", &fakeStore{ok: true, session: domain.SessionRecord{ID: "ao-1", ProjectID: "ao", Kind: domain.KindOrchestrator}}, CreateInput{SessionID: "ao-1", State: domain.ReportDone, Note: "done"}},
		{"bad state", worker, CreateInput{SessionID: "ao-2", State: "bad", Note: "note"}},
		{"bad PR", worker, CreateInput{SessionID: "ao-2", Outputs: []domain.ReportOutput{{Kind: domain.ReportOutputPRCreated, Reference: "https://example.com/x"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(Deps{Store: tc.store}).Create(context.Background(), tc.input)
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

type artifactCalls []string

func (a *artifactCalls) RecordReportedArtifact(_ context.Context, id domain.SessionID, reference string) {
	*a = append(*a, string(id)+" "+reference)
}

func TestCreateShowsEachReportedArtifactAfterTheReportIsStored(t *testing.T) {
	st := &fakeStore{ok: true, session: domain.SessionRecord{ID: "ao-7", ProjectID: "ao", Kind: domain.KindWorker}}
	var calls artifactCalls
	r, err := New(Deps{Store: st, Artifacts: &calls}).Create(context.Background(), CreateInput{
		SessionID: "ao-7", State: domain.ReportDone, Note: "done",
		Outputs: []domain.ReportOutput{
			{Kind: domain.ReportOutputArtifact, Reference: "report.html"},
			{Kind: domain.ReportOutputPRCreated, Reference: "https://github.com/o/r/pull/1"},
			{Kind: domain.ReportOutputArtifact, Reference: "/abs/notes.md"},
		},
	})
	if err != nil || r.ID == "" || st.created.ID != r.ID {
		t.Fatalf("report = %+v, %v", r, err)
	}
	if len(calls) != 2 || calls[0] != "ao-7 report.html" || calls[1] != "ao-7 /abs/notes.md" {
		t.Fatalf("recorded = %q, want the two artifacts in order", calls)
	}

	// A refused report shows nothing.
	calls = nil
	if _, err := New(Deps{Store: &fakeStore{}, Artifacts: &calls}).Create(context.Background(), CreateInput{
		SessionID: "ao-7", Outputs: []domain.ReportOutput{{Kind: domain.ReportOutputArtifact, Reference: "report.html"}},
	}); err == nil || len(calls) != 0 {
		t.Fatalf("unknown session: err = %v, recorded %q", err, calls)
	}
}

type notificationCalls struct {
	intents []ports.NotificationIntent
	err     error
}

func (n *notificationCalls) Notify(_ context.Context, intent ports.NotificationIntent) error {
	n.intents = append(n.intents, intent)
	return n.err
}

// A worker's done/stuck report is the only report state the user hears about
// away from the app: checkpoints are routine and needs_input already notifies
// through the session activity transition.
func TestCreateNotifiesOnlyOnDoneAndStuck(t *testing.T) {
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name  string
		state domain.ReportState
		want  domain.NotificationType
	}{
		{name: "done", state: domain.ReportDone, want: domain.NotificationSessionDone},
		{name: "stuck", state: domain.ReportStuck, want: domain.NotificationSessionStuck},
		{name: "checkpoint stays quiet", state: domain.ReportCheckpoint},
		{name: "needs input stays quiet", state: domain.ReportNeedsInput},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &fakeStore{ok: true, session: domain.SessionRecord{
				ID: "ao-7", ProjectID: "ao", Kind: domain.KindWorker, DisplayName: "Checkout flow",
			}}
			notes := &notificationCalls{}
			_, err := New(Deps{
				Store: st, Notifications: notes,
				Now:   func() time.Time { return now },
				NewID: func() string { return "rpt_1" },
			}).Create(context.Background(), CreateInput{SessionID: "ao-7", State: tc.state, Note: "worker words"})
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if len(notes.intents) != 0 {
					t.Fatalf("intents = %+v, want none", notes.intents)
				}
				return
			}
			if len(notes.intents) != 1 {
				t.Fatalf("intents = %+v, want exactly one", notes.intents)
			}
			got := notes.intents[0]
			if got.Type != tc.want {
				t.Fatalf("type = %q, want %q", got.Type, tc.want)
			}
			if got.SessionID != "ao-7" || got.ProjectID != "ao" {
				t.Fatalf("intent = %+v", got)
			}
			if got.SessionDisplayName != "Checkout flow" {
				t.Fatalf("display name = %q", got.SessionDisplayName)
			}
			if got.Detail != "worker words" {
				t.Fatalf("detail = %q, want the worker's own note", got.Detail)
			}
			if want := string(tc.want) + ":rpt_1"; got.SourceKey != want {
				t.Fatalf("sourceKey = %q, want %q", got.SourceKey, want)
			}
		})
	}
}

// The report is already durable when the notification is published, so a
// publish failure must not turn a committed report into a failed request.
func TestCreateIgnoresNotificationFailure(t *testing.T) {
	st := &fakeStore{ok: true, session: domain.SessionRecord{ID: "ao-7", ProjectID: "ao", Kind: domain.KindWorker}}
	notes := &notificationCalls{err: errors.New("push down")}
	if _, err := New(Deps{Store: st, Notifications: notes}).Create(context.Background(), CreateInput{
		SessionID: "ao-7", State: domain.ReportDone, Note: "done",
	}); err != nil {
		t.Fatalf("Create() = %v, want the report to survive a notification failure", err)
	}
	if len(notes.intents) != 1 {
		t.Fatalf("intents = %+v, want the publish to have been attempted", notes.intents)
	}
}
