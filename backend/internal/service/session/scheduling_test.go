package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// fakeSchedulingController records the wake-ups and admits the service asks
// for. The scheduler owns the real transitions; the service only delegates.
type fakeSchedulingController struct {
	notified []domain.ProjectID
	admitIDs []domain.SessionID
	force    []bool
	record   domain.SessionRecord
	err      error
}

func (f *fakeSchedulingController) Notify(projectID domain.ProjectID) {
	f.notified = append(f.notified, projectID)
}

func (f *fakeSchedulingController) Admit(_ context.Context, id domain.SessionID, force bool) (domain.SessionRecord, error) {
	f.admitIDs = append(f.admitIDs, id)
	f.force = append(f.force, force)
	if f.err != nil {
		return domain.SessionRecord{}, f.err
	}
	return f.record, nil
}

func TestCompleteRecordsCompletionIdempotently(t *testing.T) {
	st := newFakeStore()
	st.sessions["ao-1"] = dependencyWorker("ao-1")
	gate := &fakeSchedulingController{}
	svc := NewWithDeps(Deps{Store: st})
	svc.SetSchedulingController(gate)

	first, err := svc.Complete(context.Background(), "ao-1")
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if first.CompletedAt == nil {
		t.Fatal("completed session has no completedAt")
	}
	completedAt := *first.CompletedAt

	second, err := svc.Complete(context.Background(), "ao-1")
	if err != nil {
		t.Fatalf("second complete: %v", err)
	}
	if second.CompletedAt == nil || !second.CompletedAt.Equal(completedAt) {
		t.Fatalf("completedAt = %v, want the first completion %v", second.CompletedAt, completedAt)
	}
	// Both calls wake the scheduler: the second is a no-op for the fact but the
	// wake-up is how a dependent notices completion it may have missed.
	if len(gate.notified) != 2 || gate.notified[0] != "ao" {
		t.Fatalf("notified = %v, want two wake-ups for ao", gate.notified)
	}
}

func TestCompleteRejectsTerminatedSession(t *testing.T) {
	st := newFakeStore()
	rec := dependencyWorker("ao-1")
	rec.IsTerminated = true
	st.sessions["ao-1"] = rec
	gate := &fakeSchedulingController{}
	svc := NewWithDeps(Deps{Store: st})
	svc.SetSchedulingController(gate)

	_, err := svc.Complete(context.Background(), "ao-1")
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "SESSION_TERMINATED" {
		t.Fatalf("err = %v, want SESSION_TERMINATED", err)
	}
	if len(gate.notified) != 0 {
		t.Fatalf("notified = %v, want no wake-up for a rejected completion", gate.notified)
	}
}

func TestCompleteRejectsUnknownSession(t *testing.T) {
	svc := NewWithDeps(Deps{Store: newFakeStore()})
	_, err := svc.Complete(context.Background(), "ao-404")
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "SESSION_NOT_FOUND" {
		t.Fatalf("err = %v, want SESSION_NOT_FOUND", err)
	}
}

func TestAdmitRequiresAScheduler(t *testing.T) {
	svc := NewWithDeps(Deps{Store: newFakeStore()})
	_, err := svc.Admit(context.Background(), "ao-1", false)
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "SESSION_NOT_ADMITTABLE" {
		t.Fatalf("err = %v, want SESSION_NOT_ADMITTABLE when no scheduler is wired", err)
	}
}

func TestAdmitDelegatesToSchedulerAndReturnsReadModel(t *testing.T) {
	st := newFakeStore()
	st.sessions["ao-1"] = dependencyWorker("ao-1")
	gate := &fakeSchedulingController{record: domain.SessionRecord{ID: "ao-1", ProjectID: "ao"}}
	svc := NewWithDeps(Deps{Store: st})
	svc.SetSchedulingController(gate)

	sess, err := svc.Admit(context.Background(), "ao-1", true)
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	if sess.ID != "ao-1" {
		t.Fatalf("session = %s, want the reloaded read model", sess.ID)
	}
	if len(gate.admitIDs) != 1 || gate.admitIDs[0] != "ao-1" || !gate.force[0] {
		t.Fatalf("admit calls = %v force = %v, want one forced admit for ao-1", gate.admitIDs, gate.force)
	}
}

func TestAdmitPropagatesSchedulerErrors(t *testing.T) {
	st := newFakeStore()
	st.sessions["ao-1"] = dependencyWorker("ao-1")
	wantErr := apierr.Conflict("SESSION_NOT_ADMITTABLE", "This session is blocked: dependency_pending", nil)
	svc := NewWithDeps(Deps{Store: st})
	svc.SetSchedulingController(&fakeSchedulingController{err: wantErr})

	_, err := svc.Admit(context.Background(), "ao-1", false)
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want the scheduler's conflict", err)
	}
}

// Kill wakes the scheduler for the session's project so a dependent stops
// waiting on a dependency that just died.
func TestKillNotifiesScheduler(t *testing.T) {
	st := newFakeStore()
	st.sessions["ao-1"] = dependencyWorker("ao-1")
	gate := &fakeSchedulingController{}
	svc := NewWithDeps(Deps{Store: st, Manager: &fakeCommander{}})
	svc.SetSchedulingController(gate)

	if _, err := svc.Kill(context.Background(), "ao-1"); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if len(gate.notified) != 1 || gate.notified[0] != "ao" {
		t.Fatalf("notified = %v, want one wake-up for ao", gate.notified)
	}
}

func TestDependencySnapshotsDeriveDeliveryFacts(t *testing.T) {
	st := newFakeStore()
	completedAt := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	st.sessions["ao-1"] = domain.SessionRecord{
		ID: "ao-1", ProjectID: "ao", OutputType: domain.SessionOutputPR,
		SchedulingState: domain.SchedulingQueued,
	}
	st.pr["ao-1"] = domain.PRFacts{URL: "pr1", Merged: true}
	st.sessions["ao-2"] = domain.SessionRecord{
		ID: "ao-2", ProjectID: "ao", CompletedAt: &completedAt,
	}
	svc := NewWithDeps(Deps{Store: st})

	snaps, err := svc.DependencySnapshots(context.Background(), []domain.SessionID{"ao-1", "ao-2", "ao-404"})
	if err != nil {
		t.Fatalf("snapshots: %v", err)
	}
	merged := snaps["ao-1"]
	if !merged.Found || merged.SchedulingState != domain.SchedulingQueued {
		t.Fatalf("ao-1 snapshot = %+v, want found and queued", merged)
	}
	if merged.PRCount != 1 || !merged.AllPRsMerged {
		t.Fatalf("ao-1 pr facts = count:%d allMerged:%v, want one merged pr", merged.PRCount, merged.AllPRsMerged)
	}
	if !snaps["ao-2"].Found || snaps["ao-2"].CompletedAt == nil {
		t.Fatalf("ao-2 snapshot = %+v, want found with completedAt", snaps["ao-2"])
	}
	if snaps["ao-404"].Found {
		t.Fatal("a missing dependency row read as found")
	}

	empty, err := svc.DependencySnapshots(context.Background(), nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty snapshot request = %v, %v; want no reads and no error", empty, err)
	}
}

// A partially merged PR set must not read as merged: merged means every
// attributed PR is merged.
func TestDependencySnapshotsPartiallyMergedPRSet(t *testing.T) {
	st := newFakeStore()
	st.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", ProjectID: "ao", OutputType: domain.SessionOutputPR}
	st.prFacts["ao-1"] = []domain.PRFacts{
		{URL: "pr1", Merged: true},
		{URL: "pr2", Merged: false},
	}
	svc := NewWithDeps(Deps{Store: st})

	snaps, err := svc.DependencySnapshots(context.Background(), []domain.SessionID{"ao-1"})
	if err != nil {
		t.Fatalf("snapshots: %v", err)
	}
	if snaps["ao-1"].AllPRsMerged || snaps["ao-1"].PRCount != 2 {
		t.Fatalf("snapshot = %+v, want two PRs and allMerged false", snaps["ao-1"])
	}
}
