package session

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func dependencyProject(config domain.ProjectConfig) domain.ProjectRecord {
	return domain.ProjectRecord{ID: "ao", Config: config}
}

func dependencyWorker(id domain.SessionID) domain.SessionRecord {
	return domain.SessionRecord{ID: id, ProjectID: "ao", Kind: domain.KindWorker}
}

func dependencyErrorCode(t *testing.T, err error) string {
	t.Helper()
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want an *apierr.Error", err)
	}
	return apiErr.Code
}

func TestResolveSpawnDependenciesValidatesEdges(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*fakeStore)
		cfg   ports.SpawnConfig
		code  string
	}{
		{
			name: "missing",
			cfg:  ports.SpawnConfig{ProjectID: "ao", Dependencies: []domain.SessionDependencySpec{{SessionID: "ao-99"}}},
			code: "DEPENDENCY_NOT_FOUND",
		},
		{
			name: "cross project",
			setup: func(st *fakeStore) {
				dep := dependencyWorker("other-1")
				dep.ProjectID = "other"
				st.sessions[dep.ID] = dep
			},
			cfg:  ports.SpawnConfig{ProjectID: "ao", Dependencies: []domain.SessionDependencySpec{{SessionID: "other-1"}}},
			code: "DEPENDENCY_CROSS_PROJECT",
		},
		{
			name: "orchestrator dependency",
			setup: func(st *fakeStore) {
				dep := dependencyWorker("ao-1")
				dep.Kind = domain.KindOrchestrator
				st.sessions[dep.ID] = dep
			},
			cfg:  ports.SpawnConfig{ProjectID: "ao", Dependencies: []domain.SessionDependencySpec{{SessionID: "ao-1"}}},
			code: "DEPENDENCY_KIND_INVALID",
		},
		{
			name: "duplicate",
			setup: func(st *fakeStore) {
				st.sessions["ao-1"] = dependencyWorker("ao-1")
			},
			cfg: ports.SpawnConfig{ProjectID: "ao", Dependencies: []domain.SessionDependencySpec{
				{SessionID: "ao-1"}, {SessionID: "ao-1"},
			}},
			code: "DEPENDENCY_DUPLICATE",
		},
		{
			name: "over the edge cap",
			setup: func(st *fakeStore) {
				st.sessions["ao-1"] = dependencyWorker("ao-1")
				st.sessions["ao-2"] = dependencyWorker("ao-2")
			},
			cfg: ports.SpawnConfig{ProjectID: "ao", Dependencies: []domain.SessionDependencySpec{
				{SessionID: "ao-1"}, {SessionID: "ao-2"},
			}},
			code: "DEPENDENCY_LIMIT",
		},
		{
			name: "unknown required state",
			setup: func(st *fakeStore) {
				st.sessions["ao-1"] = dependencyWorker("ao-1")
			},
			cfg: ports.SpawnConfig{ProjectID: "ao", Dependencies: []domain.SessionDependencySpec{
				{SessionID: "ao-1", RequiredState: "shipped"},
			}},
			code: "DEPENDENCY_STATE_INVALID",
		},
		{
			name: "merged on a terminated session without a PR",
			setup: func(st *fakeStore) {
				dep := dependencyWorker("ao-1")
				dep.IsTerminated = true
				dep.OutputType = domain.SessionOutputNone
				st.sessions[dep.ID] = dep
			},
			cfg: ports.SpawnConfig{ProjectID: "ao", Dependencies: []domain.SessionDependencySpec{
				{SessionID: "ao-1", RequiredState: domain.DependencyMerged},
			}},
			code: "DEPENDENCY_STATE_INVALID",
		},
		{
			name: "self",
			cfg: ports.SpawnConfig{ProjectID: "ao", ResumeSessionID: "ao-5", Dependencies: []domain.SessionDependencySpec{
				{SessionID: "ao-5"},
			}},
			code: "DEPENDENCY_SELF",
		},
		{
			name: "cyclic inherited graph",
			setup: func(st *fakeStore) {
				st.sessions["ao-1"] = dependencyWorker("ao-1")
				st.sessions["ao-2"] = dependencyWorker("ao-2")
				st.dependencies["ao-1"] = []domain.SessionDependency{{SessionID: "ao-1", DependsOnSessionID: "ao-2"}}
				st.dependencies["ao-2"] = []domain.SessionDependency{{SessionID: "ao-2", DependsOnSessionID: "ao-1"}}
			},
			cfg:  ports.SpawnConfig{ProjectID: "ao", Dependencies: []domain.SessionDependencySpec{{SessionID: "ao-1"}}},
			code: "DEPENDENCY_CYCLE",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakeStore()
			if tc.setup != nil {
				tc.setup(st)
			}
			config := domain.ProjectConfig{Scheduling: domain.SchedulingConfig{MaxDependenciesPerSession: 1}}
			if tc.name != "over the edge cap" {
				config.Scheduling.MaxDependenciesPerSession = domain.DefaultMaxDependenciesPerSession
			}
			svc := NewWithDeps(Deps{Store: st})
			_, err := svc.resolveSpawnDependencies(context.Background(), tc.cfg, dependencyProject(config))
			if err == nil {
				t.Fatal("dependencies were accepted")
			}
			if got := dependencyErrorCode(t, err); got != tc.code {
				t.Fatalf("code = %s, want %s (err %v)", got, tc.code, err)
			}
		})
	}
}

func TestResolveSpawnDependenciesFillsRequiredState(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*fakeStore)
		state domain.DependencyRequiredState
		want  domain.DependencyRequiredState
	}{
		{
			name: "explicit state wins",
			setup: func(st *fakeStore) {
				st.sessions["ao-1"] = dependencyWorker("ao-1")
			},
			state: domain.DependencyReady,
			want:  domain.DependencyReady,
		},
		{
			name: "pr output defaults to merged",
			setup: func(st *fakeStore) {
				dep := dependencyWorker("ao-1")
				dep.OutputType = domain.SessionOutputPR
				st.sessions[dep.ID] = dep
			},
			want: domain.DependencyMerged,
		},
		{
			name: "artifact output defaults to completed",
			setup: func(st *fakeStore) {
				dep := dependencyWorker("ao-1")
				dep.OutputType = domain.SessionOutputArtifact
				st.sessions[dep.ID] = dep
			},
			want: domain.DependencyCompleted,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakeStore()
			tc.setup(st)
			svc := NewWithDeps(Deps{Store: st})
			specs, err := svc.resolveSpawnDependencies(context.Background(), ports.SpawnConfig{
				ProjectID:    "ao",
				Dependencies: []domain.SessionDependencySpec{{SessionID: "ao-1", RequiredState: tc.state}},
			}, dependencyProject(domain.ProjectConfig{}))
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if len(specs) != 1 || specs[0].RequiredState != tc.want {
				t.Fatalf("specs = %+v, want required state %s", specs, tc.want)
			}
		})
	}
}

func TestResolveSpawnDependenciesUsesProjectDefaultState(t *testing.T) {
	st := newFakeStore()
	dep := dependencyWorker("ao-1")
	dep.OutputType = domain.SessionOutputPR
	st.sessions[dep.ID] = dep
	svc := NewWithDeps(Deps{Store: st})
	specs, err := svc.resolveSpawnDependencies(context.Background(), ports.SpawnConfig{
		ProjectID:    "ao",
		Dependencies: []domain.SessionDependencySpec{{SessionID: "ao-1"}},
	}, dependencyProject(domain.ProjectConfig{Scheduling: domain.SchedulingConfig{DefaultDependencyState: "ready"}}))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(specs) != 1 || specs[0].RequiredState != domain.DependencyReady {
		t.Fatalf("specs = %+v, want the project default ready", specs)
	}
}

func TestResolveSpawnDependenciesRejectsStandaloneAndOrchestrator(t *testing.T) {
	svc := NewWithDeps(Deps{Store: newFakeStore()})
	for _, cfg := range []ports.SpawnConfig{
		{Kind: domain.KindWorker, Dependencies: []domain.SessionDependencySpec{{SessionID: "ao-1"}}},
		{ProjectID: "ao", Kind: domain.KindOrchestrator, Dependencies: []domain.SessionDependencySpec{{SessionID: "ao-1"}}},
	} {
		_, err := svc.resolveSpawnDependencies(context.Background(), cfg, domain.ProjectRecord{ID: "ao"})
		if err == nil || dependencyErrorCode(t, err) != "DEPENDENCY_STANDALONE_UNSUPPORTED" {
			t.Fatalf("cfg %+v: err = %v, want DEPENDENCY_STANDALONE_UNSUPPORTED", cfg, err)
		}
	}
}

// A dependent session is reserved through the manager's queued path, so the
// service must pick ReserveSpawn rather than Spawn.
func TestSpawnWithDependenciesReservesThroughManager(t *testing.T) {
	st := newFakeStore()
	upstream := dependencyWorker("ao-1")
	upstream.OutputType = domain.SessionOutputPR
	st.sessions[upstream.ID] = upstream
	st.projects["ao"] = domain.ProjectRecord{ID: "ao", Path: "/tmp/ao", Config: domain.ProjectConfig{
		Worker: domain.RoleOverride{Harness: domain.HarnessClaudeCode},
	}}
	fc := &fakeCommander{spawnRecord: domain.SessionRecord{
		ID: "ao-2", ProjectID: "ao", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		SchedulingState: domain.SchedulingBlocked, BlockedReason: string(domain.BlockedDependencyPending),
	}}
	gate := &fakeSchedulingController{}
	svc := NewWithDeps(Deps{Store: st, Manager: fc})
	svc.SetSchedulingController(gate)
	sess, _, _, err := svc.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID:    "ao",
		Kind:         domain.KindWorker,
		Harness:      domain.HarnessClaudeCode,
		Prompt:       "do it",
		Dependencies: []domain.SessionDependencySpec{{SessionID: "ao-1", RequiredState: domain.DependencyMerged}},
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if fc.reserveCalls != 1 {
		t.Fatalf("reserve calls = %d, want the queued path", fc.reserveCalls)
	}
	if !fc.spawned || len(fc.spawnedCfg.Dependencies) != 1 || fc.spawnedCfg.Dependencies[0].RequiredState != domain.DependencyMerged {
		t.Fatalf("manager config = %+v, want one merged dependency", fc.spawnedCfg)
	}
	if sess.SchedulingState != domain.SchedulingBlocked {
		t.Fatalf("session scheduling = %s, want blocked", sess.SchedulingState)
	}
	if len(gate.notified) != 1 || gate.notified[0] != "ao" {
		t.Fatalf("notified = %v, want one wake-up after the reservation", gate.notified)
	}
}
