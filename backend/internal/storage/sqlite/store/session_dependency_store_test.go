package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// queuedRecord is a reservation before any workspace exists: the row carries no
// worktree, branch, or runtime, which is what makes cancellation cleanup-free.
func queuedRecord(project string) domain.SessionRecord {
	rec := sampleRecord(project)
	rec.SchedulingState = domain.SchedulingQueued
	rec.ProvisionState = domain.SessionProvisionProvisioning
	rec.Metadata.WorkspacePath = ""
	rec.Metadata.Branch = ""
	return rec
}

// A reserved row and its edges must appear together: a scheduler-visible queued
// row with a partial edge set would be admitted early.
func TestCreateSessionWithDependenciesInsertsRowAndEdgesAtomically(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "dep")
	upstream, err := s.CreateSession(ctx, sampleRecord("dep"))
	if err != nil {
		t.Fatalf("create upstream: %v", err)
	}

	created, err := s.CreateSessionWithDependencies(ctx, queuedRecord("dep"), []domain.SessionDependency{{
		DependsOnSessionID: upstream.ID,
		RequiredState:      domain.DependencyMerged,
		CreatedAt:          time.Now().UTC(),
	}})
	if err != nil {
		t.Fatalf("create reserved session: %v", err)
	}
	if created.SchedulingState != domain.SchedulingQueued {
		t.Fatalf("created = %+v, want a queued reservation", created)
	}

	deps, err := s.ListSessionDependencies(ctx, created.ID)
	if err != nil {
		t.Fatalf("list dependencies: %v", err)
	}
	if len(deps) != 1 || deps[0].DependsOnSessionID != upstream.ID || deps[0].RequiredState != domain.DependencyMerged {
		t.Fatalf("dependencies = %+v, want one merged edge to %s", deps, upstream.ID)
	}
	dependents, err := s.ListDependentSessionIDs(ctx, upstream.ID)
	if err != nil || len(dependents) != 1 || dependents[0] != created.ID {
		t.Fatalf("dependents = %v err=%v, want %s", dependents, err, created.ID)
	}
}

// A rejected edge must roll the session row back too, otherwise the caller gets
// an orphan queued session it can never identify.
func TestCreateSessionWithDependenciesRollsBackOnRejectedEdge(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "dep")
	upstream, err := s.CreateSession(ctx, sampleRecord("dep"))
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.ListSessions(ctx, domain.ProjectID("dep"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.CreateSessionWithDependencies(ctx, queuedRecord("dep"), []domain.SessionDependency{{
		DependsOnSessionID: upstream.ID,
		// Not a valid CHECK value.
		RequiredState: domain.DependencyRequiredState("shipped"),
		CreatedAt:     time.Now().UTC(),
	}})
	if err == nil {
		t.Fatal("invalid required state was accepted")
	}
	after, err := s.ListSessions(ctx, domain.ProjectID("dep"))
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("sessions after failed create = %d, want %d: the row outlived its rejected edge", len(after), len(before))
	}
}

func TestSessionDependencyRejectsDuplicateAndSelfEdges(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "dep")
	upstream, err := s.CreateSession(ctx, sampleRecord("dep"))
	if err != nil {
		t.Fatal(err)
	}
	edge := domain.SessionDependency{
		DependsOnSessionID: upstream.ID,
		RequiredState:      domain.DependencyReady,
		CreatedAt:          time.Now().UTC(),
	}
	created, err := s.CreateSessionWithDependencies(ctx, queuedRecord("dep"), []domain.SessionDependency{edge})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSessionWithDependencies(ctx, queuedRecord("dep"), []domain.SessionDependency{edge, edge}); err == nil {
		t.Fatal("duplicate edge was accepted")
	}
	// A self edge is impossible through this API: the store forces every edge's
	// session id to the freshly generated id, so depends_on can never equal it.
	// The CHECK remains the storage backstop for any future edge writer.
	deps, err := s.ListSessionDependencies(ctx, created.ID)
	if err != nil || len(deps) != 1 {
		t.Fatalf("edges after rejected duplicate = %+v err=%v, want the original edge", deps, err)
	}
}

func TestQueuedSessionAdmitLaunchAndCancelTransitions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "dep")
	rec := queuedRecord("dep")
	created, err := s.CreateSessionWithDependencies(ctx, rec, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)

	// A queued session holds no scheduling slot.
	active, err := s.CountActiveProjectSessions(ctx, domain.ProjectID("dep"))
	if err != nil {
		t.Fatal(err)
	}
	if active != 0 {
		t.Fatalf("active sessions = %d, want 0 while queued", active)
	}
	baseSeq, err := s.LatestSeq(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := s.AdmitSession(ctx, created.ID, now, false)
	if err != nil || !ok {
		t.Fatalf("admit: ok=%v err=%v", ok, err)
	}
	admitted, found, err := s.GetSession(ctx, created.ID)
	if err != nil || !found || admitted.SchedulingState != domain.SchedulingAdmitted || admitted.AdmittedAt == nil {
		t.Fatalf("admitted = %+v found=%v err=%v", admitted, found, err)
	}
	if active, _ := s.CountActiveProjectSessions(ctx, domain.ProjectID("dep")); active != 1 {
		t.Fatalf("active sessions = %d, want 1 after admission", active)
	}

	// The scheduling transition must reach the event stream.
	events, err := s.EventsAfter(ctx, baseSeq, 100)
	if err != nil {
		t.Fatal(err)
	}
	var sawScheduling bool
	for _, event := range events {
		if event.Type == "session_updated" && strings.Contains(string(event.Payload), `"schedulingState":"admitted"`) {
			sawScheduling = true
		}
	}
	if !sawScheduling {
		t.Fatalf("admission did not publish a scheduling event: %+v", events)
	}

	if ok, err := s.MarkSessionSchedulingLaunched(ctx, created.ID, now); err != nil || !ok {
		t.Fatalf("mark launched: ok=%v err=%v", ok, err)
	}
	launched, _, err := s.GetSession(ctx, created.ID)
	if err != nil || launched.SchedulingState != domain.SchedulingImmediate {
		t.Fatalf("launched = %+v err=%v, want immediate", launched, err)
	}

	// Once admitted, the pre-launch cancel no longer applies: kill owns it.
	if ok, err := s.CancelQueuedSession(ctx, created.ID, now); err != nil || ok {
		t.Fatalf("cancel admitted session: ok=%v err=%v, want a no-op", ok, err)
	}
}

func TestCancelQueuedSessionTerminatesWithoutWorkspace(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "dep")
	created, err := s.CreateSessionWithDependencies(ctx, queuedRecord("dep"), nil)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := s.CancelQueuedSession(ctx, created.ID, time.Now().UTC())
	if err != nil || !ok {
		t.Fatalf("cancel: ok=%v err=%v", ok, err)
	}
	got, _, err := s.GetSession(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsTerminated || got.SchedulingState != domain.SchedulingCancelled {
		t.Fatalf("cancelled = %+v, want terminated and cancelled", got)
	}
	if got.Metadata.WorkspacePath != "" || got.Metadata.RuntimeHandleID != "" {
		t.Fatalf("cancel touched workspace facts: %+v", got.Metadata)
	}
}

func TestSchedulingCandidatesAreFIFOAndSkipLaunchFailures(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "dep")
	first, err := s.CreateSessionWithDependencies(ctx, queuedRecord("dep"), nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateSessionWithDependencies(ctx, queuedRecord("dep"), nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if ok, err := s.AdmitSession(ctx, second.ID, now, false); err != nil || !ok {
		t.Fatalf("admit: ok=%v err=%v", ok, err)
	}
	if ok, err := s.SetSessionLaunchFailed(ctx, second.ID, "boom", now); err != nil || !ok {
		t.Fatalf("launch failure: ok=%v err=%v", ok, err)
	}

	ids, err := s.ListSchedulingCandidateIDs(ctx, domain.ProjectID("dep"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != first.ID {
		t.Fatalf("candidates = %v, want only %s", ids, first.ID)
	}
	projects, err := s.ListSchedulingProjectIDs(ctx)
	if err != nil || len(projects) != 1 || projects[0] != domain.ProjectID("dep") {
		t.Fatalf("scheduling projects = %v err=%v", projects, err)
	}

	// A user retry returns the launch failure to the queue.
	if ok, err := s.ResetSessionForLaunchRetry(ctx, second.ID, now); err != nil || !ok {
		t.Fatalf("reset retry: ok=%v err=%v", ok, err)
	}
	retried, _, err := s.GetSession(ctx, second.ID)
	if err != nil || retried.SchedulingState != domain.SchedulingQueued || retried.BlockedReason != "" {
		t.Fatalf("retried = %+v err=%v, want queued", retried, err)
	}
}

func TestSetSessionCompletedIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "dep")
	created, err := s.CreateSession(ctx, sampleRecord("dep"))
	if err != nil {
		t.Fatal(err)
	}
	first := time.Now().UTC().Truncate(time.Second)
	if ok, err := s.SetSessionCompleted(ctx, created.ID, first); err != nil || !ok {
		t.Fatalf("complete: ok=%v err=%v", ok, err)
	}
	if _, err := s.SetSessionCompleted(ctx, created.ID, first.Add(time.Hour)); err != nil {
		t.Fatalf("second complete: %v", err)
	}
	got, _, err := s.GetSession(ctx, created.ID)
	if err != nil || got.CompletedAt == nil {
		t.Fatalf("completedAt = %v err=%v", got.CompletedAt, err)
	}
	if !got.CompletedAt.Equal(first) {
		t.Fatalf("completedAt = %v, want the first timestamp %v", got.CompletedAt, first)
	}
}

func TestQueuedSpawnConfigRoundTripsWithoutSerializing(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "dep")
	created, err := s.CreateSession(ctx, sampleRecord("dep"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := s.SetSessionQueuedSpawnConfig(ctx, created.ID, `{"prompt":"do it"}`, now); err != nil {
		t.Fatalf("store config: %v", err)
	}
	got, _, err := s.GetSession(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.QueuedSpawnConfig != `{"prompt":"do it"}` {
		t.Fatalf("queued spawn config = %q", got.QueuedSpawnConfig)
	}
}
