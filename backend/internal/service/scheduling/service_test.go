package scheduling

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// fakeStore is the durable scheduling surface with the same compare-and-set
// semantics as the SQLite store: a transition whose expected state no longer
// matches is a no-op, never an overwrite.
type fakeStore struct {
	projects map[string]domain.ProjectRecord
	sessions map[domain.SessionID]domain.SessionRecord
	edges    map[domain.SessionID][]domain.SessionDependency
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		projects: map[string]domain.ProjectRecord{},
		sessions: map[domain.SessionID]domain.SessionRecord{},
		edges:    map[domain.SessionID][]domain.SessionDependency{},
	}
}

func (f *fakeStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	rec, ok := f.sessions[id]
	return rec, ok, nil
}

func (f *fakeStore) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	p, ok := f.projects[id]
	return p, ok, nil
}

func (f *fakeStore) ListProjects(context.Context) ([]domain.ProjectRecord, error) {
	out := make([]domain.ProjectRecord, 0, len(f.projects))
	for _, p := range f.projects {
		out = append(out, p)
	}
	return out, nil
}

func (f *fakeStore) ListSchedulingCandidateIDs(_ context.Context, projectID domain.ProjectID) ([]domain.SessionID, error) {
	var out []domain.SessionID
	for _, rec := range f.sessions {
		if rec.ProjectID != projectID || rec.IsTerminated {
			continue
		}
		state := rec.SchedulingState.WithDefault()
		if state != domain.SchedulingQueued && state != domain.SchedulingBlocked {
			continue
		}
		if domain.BlockedReason(rec.BlockedReason).LaunchFailed() {
			continue
		}
		out = append(out, rec.ID)
	}
	sort.Slice(out, func(i, j int) bool {
		left, right := f.sessions[out[i]], f.sessions[out[j]]
		if !left.CreatedAt.Equal(right.CreatedAt) {
			return left.CreatedAt.Before(right.CreatedAt)
		}
		return left.ID < right.ID
	})
	return out, nil
}

func (f *fakeStore) ListSchedulingProjectIDs(context.Context) ([]domain.ProjectID, error) {
	seen := map[domain.ProjectID]bool{}
	for _, rec := range f.sessions {
		state := rec.SchedulingState.WithDefault()
		if rec.IsTerminated || (state != domain.SchedulingQueued && state != domain.SchedulingBlocked) {
			continue
		}
		seen[rec.ProjectID] = true
	}
	out := make([]domain.ProjectID, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	return out, nil
}

func (f *fakeStore) ListSessions(_ context.Context, projectID domain.ProjectID) ([]domain.SessionRecord, error) {
	var out []domain.SessionRecord
	for _, rec := range f.sessions {
		if rec.ProjectID != projectID {
			continue
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (f *fakeStore) ListSessionDependencies(_ context.Context, id domain.SessionID) ([]domain.SessionDependency, error) {
	return append([]domain.SessionDependency(nil), f.edges[id]...), nil
}

func (f *fakeStore) ListSessionDependenciesForSessions(_ context.Context, ids []domain.SessionID) (map[domain.SessionID][]domain.SessionDependency, error) {
	out := make(map[domain.SessionID][]domain.SessionDependency, len(ids))
	for _, id := range ids {
		out[id] = append([]domain.SessionDependency(nil), f.edges[id]...)
	}
	return out, nil
}

func (f *fakeStore) CountActiveProjectSessions(_ context.Context, projectID domain.ProjectID) (int64, error) {
	var count int64
	for _, rec := range f.sessions {
		if rec.ProjectID != projectID || rec.IsTerminated {
			continue
		}
		if rec.SchedulingState.CountsAsActive() {
			count++
		}
	}
	return count, nil
}

func (f *fakeStore) SetSessionSchedulingState(_ context.Context, id domain.SessionID, expected, next domain.SchedulingState, reason domain.BlockedReason, now time.Time) (bool, error) {
	rec, ok := f.sessions[id]
	if !ok || rec.SchedulingState.WithDefault() != expected.WithDefault() {
		return false, nil
	}
	rec.SchedulingState = next
	rec.BlockedReason = string(reason)
	rec.UpdatedAt = now
	f.sessions[id] = rec
	return true, nil
}

func (f *fakeStore) AdmitSession(_ context.Context, id domain.SessionID, at time.Time, override bool) (bool, error) {
	rec, ok := f.sessions[id]
	if !ok {
		return false, nil
	}
	state := rec.SchedulingState.WithDefault()
	if state != domain.SchedulingQueued && state != domain.SchedulingBlocked {
		return false, nil
	}
	rec.SchedulingState = domain.SchedulingAdmitted
	rec.BlockedReason = ""
	rec.AdmittedAt = &at
	rec.AdmissionOverride = rec.AdmissionOverride || override
	rec.ProvisionState = domain.SessionProvisionProvisioning
	rec.UpdatedAt = at
	f.sessions[id] = rec
	return true, nil
}

func (f *fakeStore) SetSessionAdmissionOverride(_ context.Context, id domain.SessionID, now time.Time) (bool, error) {
	rec, ok := f.sessions[id]
	if !ok || rec.AdmissionOverride {
		return false, nil
	}
	rec.AdmissionOverride = true
	rec.UpdatedAt = now
	f.sessions[id] = rec
	return true, nil
}

func (f *fakeStore) MarkSessionSchedulingLaunched(_ context.Context, id domain.SessionID, now time.Time) (bool, error) {
	rec, ok := f.sessions[id]
	if !ok || rec.SchedulingState.WithDefault() != domain.SchedulingAdmitted {
		return false, nil
	}
	rec.SchedulingState = domain.SchedulingImmediate
	rec.BlockedReason = ""
	rec.ProvisionState = domain.SessionProvisionReady
	rec.ProvisionError = ""
	rec.UpdatedAt = now
	f.sessions[id] = rec
	return true, nil
}

func (f *fakeStore) SetSessionLaunchFailed(_ context.Context, id domain.SessionID, provisionError string, now time.Time) (bool, error) {
	rec, ok := f.sessions[id]
	if !ok || rec.SchedulingState.WithDefault() != domain.SchedulingAdmitted {
		return false, nil
	}
	rec.SchedulingState = domain.SchedulingBlocked
	rec.BlockedReason = string(domain.BlockedLaunchFailed)
	rec.ProvisionState = domain.SessionProvisionFailed
	rec.ProvisionError = provisionError
	rec.UpdatedAt = now
	f.sessions[id] = rec
	return true, nil
}

func (f *fakeStore) ListAdmittedSessionsWithoutWorkspace(_ context.Context, projectID domain.ProjectID) ([]domain.SessionID, error) {
	var out []domain.SessionID
	for _, rec := range f.sessions {
		if rec.ProjectID != projectID || rec.IsTerminated {
			continue
		}
		if rec.SchedulingState.WithDefault() != domain.SchedulingAdmitted {
			continue
		}
		if rec.Metadata.WorkspacePath != "" || rec.Metadata.RuntimeHandleID != "" {
			continue
		}
		out = append(out, rec.ID)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

func (f *fakeStore) ListAdmittedSessionsWithWorkspace(_ context.Context, projectID domain.ProjectID) ([]domain.SessionID, error) {
	var out []domain.SessionID
	for _, rec := range f.sessions {
		if rec.ProjectID != projectID || rec.IsTerminated {
			continue
		}
		if rec.SchedulingState.WithDefault() != domain.SchedulingAdmitted {
			continue
		}
		if rec.Metadata.WorkspacePath == "" && rec.Metadata.RuntimeHandleID == "" {
			continue
		}
		out = append(out, rec.ID)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

type fakeLauncher struct {
	launched []domain.SessionID
	err      error
}

func (l *fakeLauncher) LaunchSpawn(_ context.Context, id domain.SessionID) (domain.SessionRecord, error) {
	if l.err != nil {
		return domain.SessionRecord{}, l.err
	}
	l.launched = append(l.launched, id)
	return domain.SessionRecord{ID: id}, nil
}

type fakeSnapshots struct {
	facts map[domain.SessionID]domain.DependencySnapshot
}

func (f *fakeSnapshots) DependencySnapshots(_ context.Context, ids []domain.SessionID) (map[domain.SessionID]domain.DependencySnapshot, error) {
	out := make(map[domain.SessionID]domain.DependencySnapshot, len(ids))
	for _, id := range ids {
		facts, ok := f.facts[id]
		if !ok {
			facts = domain.DependencySnapshot{Found: true, SchedulingState: domain.SchedulingImmediate}
		}
		out[id] = facts
	}
	return out, nil
}

func newScheduler(st *fakeStore, launcher *fakeLauncher, facts map[domain.SessionID]domain.DependencySnapshot) *Service {
	return New(Deps{
		Store:     st,
		Launcher:  launcher,
		Snapshots: &fakeSnapshots{facts: facts},
		Clock:     func() time.Time { return time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC) },
	})
}

func seedProject(st *fakeStore, id domain.ProjectID, cfg domain.SchedulingConfig) {
	st.projects[string(id)] = domain.ProjectRecord{ID: string(id), Config: domain.ProjectConfig{Scheduling: cfg}}
}

func seedQueued(st *fakeStore, id domain.SessionID, project domain.ProjectID, created time.Time, deps ...domain.SessionDependency) {
	st.sessions[id] = domain.SessionRecord{
		ID:              id,
		ProjectID:       project,
		Kind:            domain.KindWorker,
		SchedulingState: domain.SchedulingBlocked,
		BlockedReason:   string(domain.BlockedDependencyPending),
		ProvisionState:  domain.SessionProvisionProvisioning,
		CreatedAt:       created,
		UpdatedAt:       created,
	}
	if len(deps) > 0 {
		st.edges[id] = deps
	}
}

func edge(dep domain.SessionID, state domain.DependencyRequiredState) domain.SessionDependency {
	return domain.SessionDependency{SessionID: "dependent", DependsOnSessionID: dep, RequiredState: state}
}

func TestReconcileAdmitsAndLaunchesSatisfiedDependency(t *testing.T) {
	st := newFakeStore()
	launcher := &fakeLauncher{}
	seedProject(st, "ao", domain.SchedulingConfig{})
	seedQueued(st, "ao-2", "ao", time.Now(), edge("ao-1", domain.DependencyMerged))
	sched := newScheduler(st, launcher, map[domain.SessionID]domain.DependencySnapshot{
		"ao-1": {Found: true, SchedulingState: domain.SchedulingImmediate, OutputType: domain.SessionOutputPR, PRCount: 1, AllPRsMerged: true},
	})

	res, err := sched.Reconcile(context.Background(), "ao")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(res.Admitted) != 1 || res.Admitted[0] != "ao-2" {
		t.Fatalf("admitted = %v, want ao-2", res.Admitted)
	}
	if len(launcher.launched) != 1 || launcher.launched[0] != "ao-2" {
		t.Fatalf("launched = %v, want ao-2", launcher.launched)
	}
	got := st.sessions["ao-2"]
	if got.SchedulingState != domain.SchedulingImmediate {
		t.Fatalf("scheduling state = %s, want immediate after a successful launch", got.SchedulingState)
	}
	if got.ProvisionState != domain.SessionProvisionReady {
		t.Fatalf("provision state = %s, want ready after a successful launch", got.ProvisionState)
	}
}

func TestReconcileBlocksWithEachDependencyReason(t *testing.T) {
	cases := []struct {
		name string
		fact domain.DependencySnapshot
		edge domain.DependencyRequiredState
		want domain.BlockedReason
	}{
		{
			name: "missing",
			fact: domain.DependencySnapshot{},
			edge: domain.DependencyReady,
			want: domain.BlockedDependencyMissing,
		},
		{
			name: "cancelled",
			fact: domain.DependencySnapshot{Found: true, SchedulingState: domain.SchedulingCancelled, IsTerminated: true},
			edge: domain.DependencyReady,
			want: domain.BlockedDependencyCancelled,
		},
		{
			name: "terminated without success",
			fact: domain.DependencySnapshot{Found: true, SchedulingState: domain.SchedulingImmediate, IsTerminated: true, KanbanColumn: domain.KanbanBuilding},
			edge: domain.DependencyReady,
			want: domain.BlockedDependencyFailed,
		},
		{
			name: "still pending",
			fact: domain.DependencySnapshot{Found: true, SchedulingState: domain.SchedulingImmediate, KanbanColumn: domain.KanbanBuilding, OutputType: domain.SessionOutputPR},
			edge: domain.DependencyMerged,
			want: domain.BlockedDependencyPending,
		},
		{
			name: "queued dependency is pending, not failed",
			fact: domain.DependencySnapshot{Found: true, SchedulingState: domain.SchedulingQueued},
			edge: domain.DependencyCompleted,
			want: domain.BlockedDependencyPending,
		},
		{
			name: "cannot produce a pr",
			fact: domain.DependencySnapshot{Found: true, SchedulingState: domain.SchedulingImmediate, OutputType: domain.SessionOutputNone},
			edge: domain.DependencyMerged,
			want: domain.BlockedDependencyUnsatisfiable,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakeStore()
			launcher := &fakeLauncher{}
			seedProject(st, "ao", domain.SchedulingConfig{})
			seedQueued(st, "ao-2", "ao", time.Now(), edge("ao-1", tc.edge))
			sched := newScheduler(st, launcher, map[domain.SessionID]domain.DependencySnapshot{"ao-1": tc.fact})

			res, err := sched.Reconcile(context.Background(), "ao")
			if err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			if len(res.Admitted) != 0 || len(launcher.launched) != 0 {
				t.Fatalf("admitted=%v launched=%v, want none", res.Admitted, launcher.launched)
			}
			got := st.sessions["ao-2"]
			if got.SchedulingState != domain.SchedulingBlocked || domain.BlockedReason(got.BlockedReason) != tc.want {
				t.Fatalf("state=%s reason=%q, want blocked/%s", got.SchedulingState, got.BlockedReason, tc.want)
			}
		})
	}
}

func TestReconcileHardFailureOutranksPending(t *testing.T) {
	st := newFakeStore()
	launcher := &fakeLauncher{}
	seedProject(st, "ao", domain.SchedulingConfig{})
	seedQueued(st, "ao-3", "ao", time.Now(),
		edge("ao-1", domain.DependencyReady), edge("ao-2", domain.DependencyMerged))
	sched := newScheduler(st, launcher, map[domain.SessionID]domain.DependencySnapshot{
		"ao-1": {Found: true, SchedulingState: domain.SchedulingImmediate, KanbanColumn: domain.KanbanBuilding},
		"ao-2": {Found: true, SchedulingState: domain.SchedulingCancelled, IsTerminated: true},
	})
	if _, err := sched.Reconcile(context.Background(), "ao"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got := st.sessions["ao-3"]; domain.BlockedReason(got.BlockedReason) != domain.BlockedDependencyCancelled {
		t.Fatalf("blocked reason = %q, want the hard failure dependency_cancelled", got.BlockedReason)
	}
}

func TestReconcileAdmitsInFIFOOrderWithinTheProjectLimit(t *testing.T) {
	st := newFakeStore()
	launcher := &fakeLauncher{}
	seedProject(st, "ao", domain.SchedulingConfig{MaxActiveSessions: 1})
	base := time.Now()
	seedQueued(st, "ao-1", "ao", base)
	seedQueued(st, "ao-2", "ao", base.Add(time.Second))
	seedQueued(st, "ao-3", "ao", base.Add(2*time.Second))
	sched := newScheduler(st, launcher, nil)

	res, err := sched.Reconcile(context.Background(), "ao")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(res.Admitted) != 1 || res.Admitted[0] != "ao-1" {
		t.Fatalf("admitted = %v, want only the oldest ao-1", res.Admitted)
	}
	for _, id := range []domain.SessionID{"ao-2", "ao-3"} {
		if got := st.sessions[id]; got.SchedulingState != domain.SchedulingBlocked || domain.BlockedReason(got.BlockedReason) != domain.BlockedProjectLimit {
			t.Fatalf("%s state=%s reason=%q, want blocked/project_limit", id, got.SchedulingState, got.BlockedReason)
		}
	}
	// A second pass with no free slot must not churn the queue into a launch.
	if _, err := sched.Reconcile(context.Background(), "ao"); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if len(launcher.launched) != 1 {
		t.Fatalf("launched = %v, want exactly one session", launcher.launched)
	}

	// Free the slot: the next oldest waiting session is admitted.
	first := st.sessions["ao-1"]
	first.IsTerminated = true
	st.sessions["ao-1"] = first
	res, err = sched.Reconcile(context.Background(), "ao")
	if err != nil {
		t.Fatalf("third reconcile: %v", err)
	}
	if len(res.Admitted) != 1 || res.Admitted[0] != "ao-2" {
		t.Fatalf("admitted after a freed slot = %v, want ao-2", res.Admitted)
	}
}

func TestReconcileNeverRetriesLaunchFailure(t *testing.T) {
	st := newFakeStore()
	launcher := &fakeLauncher{}
	seedProject(st, "ao", domain.SchedulingConfig{})
	seedQueued(st, "ao-2", "ao", time.Now())
	rec := st.sessions["ao-2"]
	rec.SchedulingState = domain.SchedulingBlocked
	rec.BlockedReason = string(domain.BlockedLaunchFailed)
	st.sessions["ao-2"] = rec
	sched := newScheduler(st, launcher, nil)

	if _, err := sched.Reconcile(context.Background(), "ao"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(launcher.launched) != 0 {
		t.Fatalf("launched = %v, want none: launch_failed never retries automatically", launcher.launched)
	}
	if got := st.sessions["ao-2"]; domain.BlockedReason(got.BlockedReason) != domain.BlockedLaunchFailed {
		t.Fatalf("blocked reason = %q, want launch_failed", got.BlockedReason)
	}
}

func TestReconcileRecordsLaunchFailure(t *testing.T) {
	st := newFakeStore()
	launcher := &fakeLauncher{err: errors.New("worktree creation failed")}
	seedProject(st, "ao", domain.SchedulingConfig{})
	seedQueued(st, "ao-2", "ao", time.Now())
	sched := newScheduler(st, launcher, nil)

	res, err := sched.Reconcile(context.Background(), "ao")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(res.Failed) != 1 || res.Failed[0] != "ao-2" {
		t.Fatalf("failed = %v, want ao-2", res.Failed)
	}
	got := st.sessions["ao-2"]
	if got.SchedulingState != domain.SchedulingBlocked || domain.BlockedReason(got.BlockedReason) != domain.BlockedLaunchFailed {
		t.Fatalf("state=%s reason=%q, want blocked/launch_failed", got.SchedulingState, got.BlockedReason)
	}
	if !strings.Contains(got.ProvisionError, "worktree creation failed") {
		t.Fatalf("provision error = %q, want the launch failure", got.ProvisionError)
	}
}

func TestReconcileRecoversAdmittedSessionWithoutWorkspace(t *testing.T) {
	st := newFakeStore()
	launcher := &fakeLauncher{}
	seedProject(st, "ao", domain.SchedulingConfig{})
	admittedAt := time.Now().Add(-time.Minute)
	st.sessions["ao-9"] = domain.SessionRecord{
		ID: "ao-9", ProjectID: "ao", Kind: domain.KindWorker,
		SchedulingState: domain.SchedulingAdmitted, AdmittedAt: &admittedAt, CreatedAt: admittedAt,
	}
	sched := newScheduler(st, launcher, nil)

	if _, err := sched.Reconcile(context.Background(), "ao"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(launcher.launched) != 1 || launcher.launched[0] != "ao-9" {
		t.Fatalf("launched = %v, want the admitted row relaunched once", launcher.launched)
	}
}

// A queued reservation that launches ends ready, and a daemon restart must not
// fail it or launch it a second time.
func TestReconcileLaunchTransitionSurvivesRestart(t *testing.T) {
	st := newFakeStore()
	launcher := &fakeLauncher{}
	seedProject(st, "ao", domain.SchedulingConfig{})
	seedQueued(st, "ao-2", "ao", time.Now(), edge("ao-1", domain.DependencyMerged))
	facts := map[domain.SessionID]domain.DependencySnapshot{
		"ao-1": {Found: true, SchedulingState: domain.SchedulingImmediate, OutputType: domain.SessionOutputPR, PRCount: 1, AllPRsMerged: true},
	}
	sched := newScheduler(st, launcher, facts)
	if _, err := sched.Reconcile(context.Background(), "ao"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(launcher.launched) != 1 || launcher.launched[0] != "ao-2" {
		t.Fatalf("launched = %v, want exactly one launch", launcher.launched)
	}
	launched := st.sessions["ao-2"]
	if launched.SchedulingState != domain.SchedulingImmediate || launched.ProvisionState != domain.SessionProvisionReady {
		t.Fatalf("after launch state=%s provision=%s, want immediate/ready", launched.SchedulingState, launched.ProvisionState)
	}

	// Simulate a daemon restart: a new scheduler and launcher over the same
	// durable rows. The launched row must stay ready and must not relaunch.
	restartedLauncher := &fakeLauncher{}
	restarted := newScheduler(st, restartedLauncher, facts)
	if _, err := restarted.Reconcile(context.Background(), "ao"); err != nil {
		t.Fatalf("reconcile after restart: %v", err)
	}
	if len(restartedLauncher.launched) != 0 {
		t.Fatalf("relaunched = %v, want no second launch after restart", restartedLauncher.launched)
	}
	after := st.sessions["ao-2"]
	if after.SchedulingState != domain.SchedulingImmediate || after.ProvisionState != domain.SessionProvisionReady {
		t.Fatalf("after restart state=%s provision=%s, want immediate/ready", after.SchedulingState, after.ProvisionState)
	}
}

func TestReconcileFinishesAdmittedSessionWithWorkspace(t *testing.T) {
	st := newFakeStore()
	launcher := &fakeLauncher{}
	seedProject(st, "ao", domain.SchedulingConfig{})
	admittedAt := time.Now().Add(-time.Minute)
	st.sessions["ao-9"] = domain.SessionRecord{
		ID: "ao-9", ProjectID: "ao", Kind: domain.KindWorker,
		SchedulingState: domain.SchedulingAdmitted,
		ProvisionState:  domain.SessionProvisionProvisioning,
		AdmittedAt:      &admittedAt, CreatedAt: admittedAt,
		Metadata: domain.SessionMetadata{
			WorkspacePath:   "/tmp/ao-9",
			RuntimeHandleID: "ptyhost-v1:ao-9",
		},
	}
	sched := newScheduler(st, launcher, nil)

	if _, err := sched.Reconcile(context.Background(), "ao"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(launcher.launched) != 0 {
		t.Fatalf("launched = %v, want no second launch for a row that already has a workspace", launcher.launched)
	}
	got := st.sessions["ao-9"]
	if got.SchedulingState != domain.SchedulingImmediate {
		t.Fatalf("scheduling state = %s, want immediate after recovery", got.SchedulingState)
	}
	if got.ProvisionState != domain.SessionProvisionReady {
		t.Fatalf("provision state = %s, want ready after recovery", got.ProvisionState)
	}
}

func TestAdmitForceBypassesDependenciesAndRecordsOverride(t *testing.T) {
	st := newFakeStore()
	launcher := &fakeLauncher{}
	seedProject(st, "ao", domain.SchedulingConfig{MaxActiveSessions: 1})
	st.sessions["ao-busy"] = domain.SessionRecord{ID: "ao-busy", ProjectID: "ao", Kind: domain.KindWorker}
	seedQueued(st, "ao-2", "ao", time.Now(), edge("ao-1", domain.DependencyMerged))
	sched := newScheduler(st, launcher, map[domain.SessionID]domain.DependencySnapshot{
		"ao-1": {Found: true, SchedulingState: domain.SchedulingImmediate, KanbanColumn: domain.KanbanBuilding},
	})

	rec, err := sched.Admit(context.Background(), "ao-2", true)
	if err != nil {
		t.Fatalf("force admit: %v", err)
	}
	if !rec.AdmissionOverride {
		t.Fatalf("admission override = false, want a recorded override")
	}
	if rec.SchedulingState != domain.SchedulingImmediate {
		t.Fatalf("scheduling state = %s, want immediate after a forced launch", rec.SchedulingState)
	}
	if len(launcher.launched) != 1 || launcher.launched[0] != "ao-2" {
		t.Fatalf("launched = %v, want ao-2", launcher.launched)
	}
}

func TestAdmitRejectsUnsatisfiedDependencies(t *testing.T) {
	st := newFakeStore()
	launcher := &fakeLauncher{}
	seedProject(st, "ao", domain.SchedulingConfig{})
	seedQueued(st, "ao-2", "ao", time.Now(), edge("ao-1", domain.DependencyMerged))
	sched := newScheduler(st, launcher, map[domain.SessionID]domain.DependencySnapshot{
		"ao-1": {Found: true, SchedulingState: domain.SchedulingImmediate, KanbanColumn: domain.KanbanBuilding, OutputType: domain.SessionOutputPR},
	})

	rec, err := sched.Admit(context.Background(), "ao-2", false)
	if err == nil {
		t.Fatal("a non-forced admit launched a session with an unsatisfied dependency")
	}
	if len(launcher.launched) != 0 {
		t.Fatalf("launched = %v, want none", launcher.launched)
	}
	if rec.SchedulingState != domain.SchedulingBlocked || domain.BlockedReason(rec.BlockedReason) != domain.BlockedDependencyPending {
		t.Fatalf("state=%s reason=%q, want blocked/dependency_pending", rec.SchedulingState, rec.BlockedReason)
	}
}

func TestAdmitIsIdempotentOnceAdmitted(t *testing.T) {
	st := newFakeStore()
	launcher := &fakeLauncher{}
	seedProject(st, "ao", domain.SchedulingConfig{})
	seedQueued(st, "ao-2", "ao", time.Now())
	rec := st.sessions["ao-2"]
	rec.SchedulingState = domain.SchedulingImmediate
	st.sessions["ao-2"] = rec
	sched := newScheduler(st, launcher, nil)

	got, err := sched.Admit(context.Background(), "ao-2", true)
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	if got.SchedulingState != domain.SchedulingImmediate {
		t.Fatalf("state = %s, want unchanged immediate", got.SchedulingState)
	}
	if len(launcher.launched) != 0 {
		t.Fatalf("launched = %v, want none for an already-running session", launcher.launched)
	}
}

func TestEdgeSatisfiedReadyAcceptsMergedClosedAndApproved(t *testing.T) {
	cases := []struct {
		name string
		fact domain.DependencySnapshot
		want bool
	}{
		{"building", domain.DependencySnapshot{Found: true, SchedulingState: domain.SchedulingImmediate, KanbanColumn: domain.KanbanBuilding}, false},
		{"ready", domain.DependencySnapshot{Found: true, SchedulingState: domain.SchedulingImmediate, KanbanColumn: domain.KanbanReady}, true},
		{"terminated ready", domain.DependencySnapshot{Found: true, SchedulingState: domain.SchedulingImmediate, IsTerminated: true, KanbanColumn: domain.KanbanReady}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, _ := edgeSatisfied(domain.SessionDependency{RequiredState: domain.DependencyReady}, tc.fact)
			if ok != tc.want {
				t.Fatalf("satisfied = %v, want %v", ok, tc.want)
			}
		})
	}
}

func seedOrchestrator(st *fakeStore, id domain.SessionID, project domain.ProjectID, created time.Time) {
	st.sessions[id] = domain.SessionRecord{
		ID:              id,
		ProjectID:       project,
		Kind:            domain.KindOrchestrator,
		SchedulingState: domain.SchedulingImmediate,
		ProvisionState:  domain.SessionProvisionReady,
		CreatedAt:       created,
		UpdatedAt:       created,
	}
}

func seedActiveWorker(st *fakeStore, id, parent domain.SessionID, project domain.ProjectID, created time.Time) {
	st.sessions[id] = domain.SessionRecord{
		ID:              id,
		ProjectID:       project,
		ParentSessionID: parent,
		Kind:            domain.KindWorker,
		SchedulingState: domain.SchedulingImmediate,
		ProvisionState:  domain.SessionProvisionReady,
		CreatedAt:       created,
		UpdatedAt:       created,
	}
}

func seedQueuedWorker(st *fakeStore, id, parent domain.SessionID, project domain.ProjectID, created time.Time) {
	st.sessions[id] = domain.SessionRecord{
		ID:              id,
		ProjectID:       project,
		ParentSessionID: parent,
		Kind:            domain.KindWorker,
		SchedulingState: domain.SchedulingQueued,
		ProvisionState:  domain.SessionProvisionProvisioning,
		CreatedAt:       created,
		UpdatedAt:       created,
	}
}

func TestReconcileAdmitsTeamWorkersUpToTheLimit(t *testing.T) {
	st := newFakeStore()
	launcher := &fakeLauncher{}
	base := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	seedProject(st, "ao", domain.SchedulingConfig{MaxActivePerTeam: 2})
	seedOrchestrator(st, "ao-1", "ao", base)
	seedActiveWorker(st, "ao-2", "ao-1", "ao", base.Add(time.Minute))
	seedQueuedWorker(st, "ao-3", "ao-1", "ao", base.Add(2*time.Minute))
	seedQueuedWorker(st, "ao-4", "ao-1", "ao", base.Add(3*time.Minute))
	sched := newScheduler(st, launcher, nil)

	res, err := sched.Reconcile(context.Background(), "ao")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(res.Admitted) != 1 || res.Admitted[0] != "ao-3" {
		t.Fatalf("admitted = %v, want [ao-3]", res.Admitted)
	}
	if got := st.sessions["ao-4"]; got.SchedulingState != domain.SchedulingBlocked || got.BlockedReason != string(domain.BlockedTeamLimit) {
		t.Fatalf("ao-4 = %+v, want blocked(team_limit)", got)
	}
	// Admitting and blocking never touches the worker already running.
	if got := st.sessions["ao-2"]; got.SchedulingState != domain.SchedulingImmediate {
		t.Fatalf("running worker ao-2 = %s, want immediate (never paused)", got.SchedulingState)
	}
}

func TestReconcileBlocksTeamLimitWithoutTouchingRunningWorkers(t *testing.T) {
	st := newFakeStore()
	base := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	seedProject(st, "ao", domain.SchedulingConfig{MaxActivePerTeam: 1})
	seedOrchestrator(st, "ao-1", "ao", base)
	seedActiveWorker(st, "ao-2", "ao-1", "ao", base.Add(time.Minute))
	seedQueuedWorker(st, "ao-3", "ao-1", "ao", base.Add(2*time.Minute))
	sched := newScheduler(st, &fakeLauncher{}, nil)

	res, err := sched.Reconcile(context.Background(), "ao")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(res.Admitted) != 0 {
		t.Fatalf("admitted = %v, want none", res.Admitted)
	}
	if got := st.sessions["ao-3"]; got.BlockedReason != string(domain.BlockedTeamLimit) {
		t.Fatalf("ao-3 reason = %q, want team_limit", got.BlockedReason)
	}
	if got := st.sessions["ao-2"]; got.SchedulingState != domain.SchedulingImmediate || got.IsTerminated {
		t.Fatalf("running worker = %+v, want untouched", got)
	}
}

func TestReconcileReleasesTeamLimitWhenWorkerTerminates(t *testing.T) {
	st := newFakeStore()
	base := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	seedProject(st, "ao", domain.SchedulingConfig{MaxActivePerTeam: 1})
	seedOrchestrator(st, "ao-1", "ao", base)
	seedActiveWorker(st, "ao-2", "ao-1", "ao", base.Add(time.Minute))
	seedQueuedWorker(st, "ao-3", "ao-1", "ao", base.Add(2*time.Minute))
	sched := newScheduler(st, &fakeLauncher{}, nil)

	if _, err := sched.Reconcile(context.Background(), "ao"); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	if got := st.sessions["ao-3"]; got.BlockedReason != string(domain.BlockedTeamLimit) {
		t.Fatalf("ao-3 after first reconcile = %+v, want blocked(team_limit)", got)
	}

	worker := st.sessions["ao-2"]
	worker.IsTerminated = true
	st.sessions["ao-2"] = worker

	res, err := sched.Reconcile(context.Background(), "ao")
	if err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if len(res.Admitted) != 1 || res.Admitted[0] != "ao-3" {
		t.Fatalf("admitted after termination = %v, want [ao-3]", res.Admitted)
	}
}

func TestReconcileTeamLimitsAreIndependentPerTeam(t *testing.T) {
	st := newFakeStore()
	base := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	seedProject(st, "ao", domain.SchedulingConfig{MaxActivePerTeam: 1})
	seedOrchestrator(st, "ao-1", "ao", base)
	seedOrchestrator(st, "ao-2", "ao", base.Add(time.Second))
	seedActiveWorker(st, "ao-1-a", "ao-1", "ao", base.Add(time.Minute))
	seedQueuedWorker(st, "ao-1-b", "ao-1", "ao", base.Add(2*time.Minute))
	// The second team has a free slot even though the first team is full.
	seedQueuedWorker(st, "ao-2-a", "ao-2", "ao", base.Add(3*time.Minute))
	sched := newScheduler(st, &fakeLauncher{}, nil)

	res, err := sched.Reconcile(context.Background(), "ao")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(res.Admitted) != 1 || res.Admitted[0] != "ao-2-a" {
		t.Fatalf("admitted = %v, want [ao-2-a]", res.Admitted)
	}
	if got := st.sessions["ao-1-b"]; got.SchedulingState != domain.SchedulingBlocked || got.BlockedReason != string(domain.BlockedTeamLimit) {
		t.Fatalf("ao-1-b = %+v, want blocked(team_limit)", got)
	}
	if got := st.sessions["ao-1-a"]; got.SchedulingState != domain.SchedulingImmediate {
		t.Fatalf("running worker ao-1-a = %s, want untouched", got.SchedulingState)
	}
}

func TestReconcileNestedOrchestratorRunsItsOwnTeamLimit(t *testing.T) {
	st := newFakeStore()
	base := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	seedProject(st, "ao", domain.SchedulingConfig{MaxActivePerTeam: 1})
	seedOrchestrator(st, "ao-1", "ao", base)
	seedActiveWorker(st, "ao-1-w", "ao-1", "ao", base.Add(time.Minute))
	// A nested orchestrator is a lead, not a worker of the outer team, so the
	// outer team's full slot count must not block the inner team's first worker.
	seedOrchestrator(st, "ao-2", "ao", base.Add(2*time.Minute))
	stWorker := st.sessions["ao-2"]
	stWorker.ParentSessionID = "ao-1"
	stWorker.CreatedAt = base.Add(2 * time.Minute)
	st.sessions["ao-2"] = stWorker
	seedQueuedWorker(st, "ao-2-w", "ao-2", "ao", base.Add(3*time.Minute))
	sched := newScheduler(st, &fakeLauncher{}, nil)

	res, err := sched.Reconcile(context.Background(), "ao")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(res.Admitted) != 1 || res.Admitted[0] != "ao-2-w" {
		t.Fatalf("admitted = %v, want [ao-2-w]", res.Admitted)
	}
}

func TestReconcileTeamLimitSkipsStandaloneWorkers(t *testing.T) {
	st := newFakeStore()
	base := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	seedProject(st, "ao", domain.SchedulingConfig{MaxActivePerTeam: 1})
	seedOrchestrator(st, "ao-1", "ao", base)
	seedActiveWorker(st, "ao-2", "ao-1", "ao", base.Add(time.Minute))
	// A standalone worker has no orchestrator ancestor: desktop New Task, a
	// cloud task, or an unattached session must never be team-limited.
	seedQueuedWorker(st, "ao-3", "", "ao", base.Add(2*time.Minute))
	sched := newScheduler(st, &fakeLauncher{}, nil)

	res, err := sched.Reconcile(context.Background(), "ao")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(res.Admitted) != 1 || res.Admitted[0] != "ao-3" {
		t.Fatalf("admitted = %v, want [ao-3]", res.Admitted)
	}
}

func TestReconcileUsesDefaultTeamLimitWhenConfigIsUnset(t *testing.T) {
	st := newFakeStore()
	base := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	seedProject(st, "ao", domain.SchedulingConfig{})
	seedOrchestrator(st, "ao-1", "ao", base)
	for i := 0; i < domain.DefaultMaxActivePerTeam; i++ {
		seedActiveWorker(st, domain.SessionID("ao-"+string(rune('a'+i))), "ao-1", "ao", base.Add(time.Duration(i+1)*time.Minute))
	}
	seedQueuedWorker(st, "ao-queued", "ao-1", "ao", base.Add(time.Hour))
	sched := newScheduler(st, &fakeLauncher{}, nil)

	res, err := sched.Reconcile(context.Background(), "ao")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(res.Admitted) != 0 {
		t.Fatalf("admitted = %v, want none at the default limit", res.Admitted)
	}
	if got := st.sessions["ao-queued"]; got.BlockedReason != string(domain.BlockedTeamLimit) {
		t.Fatalf("queued reason = %q, want team_limit", got.BlockedReason)
	}
}

func TestAdmitForceBypassesTeamLimit(t *testing.T) {
	st := newFakeStore()
	base := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	seedProject(st, "ao", domain.SchedulingConfig{MaxActivePerTeam: 1})
	seedOrchestrator(st, "ao-1", "ao", base)
	seedActiveWorker(st, "ao-2", "ao-1", "ao", base.Add(time.Minute))
	seedQueuedWorker(st, "ao-3", "ao-1", "ao", base.Add(2*time.Minute))
	sched := newScheduler(st, &fakeLauncher{}, nil)

	rec, err := sched.Admit(context.Background(), "ao-3", true)
	if err != nil {
		t.Fatalf("forced admit: %v", err)
	}
	if rec.SchedulingState != domain.SchedulingImmediate {
		t.Fatalf("state = %s, want immediate after forced launch", rec.SchedulingState)
	}
	if !rec.AdmissionOverride {
		t.Fatal("forced admit did not record the override")
	}
}

func TestEdgeSatisfiedMergedRequiresEveryAttributedPR(t *testing.T) {
	merged := domain.SessionDependency{RequiredState: domain.DependencyMerged}
	if ok, _ := edgeSatisfied(merged, domain.DependencySnapshot{Found: true, SchedulingState: domain.SchedulingImmediate, OutputType: domain.SessionOutputPR, PRCount: 2, AllPRsMerged: true}); !ok {
		t.Fatal("two merged PRs did not satisfy merged")
	}
	if ok, _ := edgeSatisfied(merged, domain.DependencySnapshot{Found: true, SchedulingState: domain.SchedulingImmediate, OutputType: domain.SessionOutputPR, PRCount: 2, AllPRsMerged: false}); ok {
		t.Fatal("a partially merged PR set satisfied merged")
	}
	if ok, _ := edgeSatisfied(merged, domain.DependencySnapshot{Found: true, SchedulingState: domain.SchedulingImmediate, OutputType: domain.SessionOutputNone}); ok {
		t.Fatal("a dependency with no PR output satisfied merged")
	}
}
