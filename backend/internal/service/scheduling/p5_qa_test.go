package scheduling

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// p5LoadFixture mirrors the checked-in load fixture. It intentionally loads
// every session as queued with no scheduling facts beyond its edges, so the
// test exercises the scheduler's own evaluation and admission path rather than
// pre-seeding the expected result.
type p5LoadFixture struct {
	ProjectID     string               `json:"projectId"`
	ProjectConfig domain.ProjectConfig `json:"projectConfig"`
	Sessions      []struct {
		ID          string `json:"id"`
		DisplayName string `json:"displayName"`
		DependsOn   []struct {
			SessionID     string                         `json:"sessionId"`
			RequiredState domain.DependencyRequiredState `json:"requiredState"`
		} `json:"dependsOn"`
	} `json:"sessions"`
}

// TestP5Load100Sessions200Edges evaluates the phase-5 load fixture through the
// real scheduling service. It checks one admission pass at the project limit,
// then a second pass that must evaluate the remaining queue without launching
// anything else.
func TestP5Load100Sessions200Edges(t *testing.T) {
	raw, err := os.ReadFile("testdata/load/project_100_sessions_200_edges.json")
	if err != nil {
		t.Fatalf("read load fixture: %v", err)
	}
	var fixture p5LoadFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("decode load fixture: %v", err)
	}

	st := newFakeStore()
	st.projects[fixture.ProjectID] = domain.ProjectRecord{
		ID:     fixture.ProjectID,
		Config: fixture.ProjectConfig,
	}
	base := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	projectID := domain.ProjectID(fixture.ProjectID)
	for i, session := range fixture.Sessions {
		id := domain.SessionID(session.ID)
		st.sessions[id] = domain.SessionRecord{
			ID:              id,
			ProjectID:       projectID,
			Kind:            domain.KindWorker,
			DisplayName:     session.DisplayName,
			SchedulingState: domain.SchedulingQueued,
			ProvisionState:  domain.SessionProvisionQueued,
			CreatedAt:       base.Add(time.Duration(i) * time.Millisecond),
			UpdatedAt:       base.Add(time.Duration(i) * time.Millisecond),
		}
		for _, dep := range session.DependsOn {
			st.edges[id] = append(st.edges[id], domain.SessionDependency{
				SessionID:          id,
				DependsOnSessionID: domain.SessionID(dep.SessionID),
				RequiredState:      dep.RequiredState,
			})
		}
	}

	launcher := &fakeLauncher{}
	facts := make(map[domain.SessionID]domain.DependencySnapshot, len(fixture.Sessions))
	completedAt := base
	for _, session := range fixture.Sessions {
		facts[domain.SessionID(session.ID)] = domain.DependencySnapshot{
			Found:           true,
			SchedulingState: domain.SchedulingImmediate,
			CompletedAt:     &completedAt,
			OutputType:      domain.SessionOutputPR,
			KanbanColumn:    domain.KanbanReady,
			PRCount:         1,
			AllPRsMerged:    true,
		}
	}
	sched := newScheduler(st, launcher, facts)
	started := time.Now()
	first, err := sched.Reconcile(context.Background(), projectID)
	if err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	firstElapsed := time.Since(started)
	if first.Evaluated != len(fixture.Sessions) {
		t.Fatalf("first evaluated = %d, want %d", first.Evaluated, len(fixture.Sessions))
	}
	if len(first.Admitted) != 20 || len(first.Launched) != 20 {
		t.Fatalf("first admitted=%d launched=%d, want 20/20", len(first.Admitted), len(first.Launched))
	}
	if firstElapsed > 15*time.Second {
		t.Fatalf("first reconcile took %s, want under 15s", firstElapsed)
	}

	secondStarted := time.Now()
	second, err := sched.Reconcile(context.Background(), projectID)
	if err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	secondElapsed := time.Since(secondStarted)
	if second.Evaluated != len(fixture.Sessions)-len(first.Admitted) {
		t.Fatalf("second evaluated = %d, want %d", second.Evaluated, len(fixture.Sessions)-len(first.Admitted))
	}
	if len(second.Admitted) != 0 || len(second.Launched) != 0 {
		t.Fatalf("second admitted=%d launched=%d, want 0/0", len(second.Admitted), len(second.Launched))
	}
	if secondElapsed > 15*time.Second {
		t.Fatalf("second reconcile took %s, want under 15s", secondElapsed)
	}
	if len(launcher.launched) != len(first.Launched) {
		t.Fatalf("launcher recorded %d launches, want %d", len(launcher.launched), len(first.Launched))
	}

	t.Logf("sessions=%d edges=%d evaluated=%d admitted=%d launched=%d elapsed_ms=%d second_evaluated=%d second_launched=%d second_elapsed_ms=%d budget_ms=15000",
		len(fixture.Sessions), countP5Edges(fixture), first.Evaluated, len(first.Admitted), len(first.Launched),
		firstElapsed.Milliseconds(), second.Evaluated, len(second.Launched), secondElapsed.Milliseconds())
}

func TestP5CrashRecoveryFourStates(t *testing.T) {
	at := time.Date(2026, 10, 31, 11, 0, 0, 0, time.UTC)
	cases := []struct {
		name         string
		subject      domain.SessionID
		seed         func(*fakeStore)
		facts        map[domain.SessionID]domain.DependencySnapshot
		wantLaunched int
		wantState    domain.SchedulingState
		wantReason   domain.BlockedReason
	}{
		{
			name:    "queued-free-slot",
			subject: "ao-1",
			seed: func(st *fakeStore) {
				st.sessions["ao-1"] = domain.SessionRecord{
					ID: "ao-1", ProjectID: "ao", Kind: domain.KindWorker,
					SchedulingState: domain.SchedulingQueued, ProvisionState: domain.SessionProvisionQueued,
					CreatedAt: at, UpdatedAt: at,
				}
			},
			wantLaunched: 1,
			wantState:    domain.SchedulingImmediate,
		},
		{
			name:    "reservation-committed-dependency-pending",
			subject: "ao-d",
			seed: func(st *fakeStore) {
				dep := domain.SessionRecord{
					ID: "ao-dep", ProjectID: "ao", Kind: domain.KindWorker,
					SchedulingState: domain.SchedulingAdmitted, ProvisionState: domain.SessionProvisionProvisioning,
					AdmittedAt: &at, CreatedAt: at, UpdatedAt: at,
				}
				dep.Metadata.WorkspacePath = "/tmp/p5-dep-ws"
				st.sessions["ao-dep"] = dep
				st.sessions["ao-d"] = domain.SessionRecord{
					ID: "ao-d", ProjectID: "ao", Kind: domain.KindWorker,
					SchedulingState: domain.SchedulingBlocked, BlockedReason: string(domain.BlockedDependencyPending),
					ProvisionState: domain.SessionProvisionQueued, CreatedAt: at, UpdatedAt: at,
				}
				st.edges["ao-d"] = []domain.SessionDependency{{
					SessionID: "ao-d", DependsOnSessionID: "ao-dep", RequiredState: domain.DependencyReady,
				}}
			},
			facts: map[domain.SessionID]domain.DependencySnapshot{
				"ao-dep": {Found: true, SchedulingState: domain.SchedulingAdmitted, KanbanColumn: domain.KanbanBuilding},
			},
			wantLaunched: 0,
			wantState:    domain.SchedulingBlocked,
			wantReason:   domain.BlockedDependencyPending,
		},
		{
			name:    "admitted-never-launched",
			subject: "ao-1",
			seed: func(st *fakeStore) {
				st.sessions["ao-1"] = domain.SessionRecord{
					ID: "ao-1", ProjectID: "ao", Kind: domain.KindWorker,
					SchedulingState: domain.SchedulingAdmitted, ProvisionState: domain.SessionProvisionProvisioning,
					AdmittedAt: &at, CreatedAt: at, UpdatedAt: at,
				}
			},
			wantLaunched: 1,
			wantState:    domain.SchedulingImmediate,
		},
		{
			name:    "admitted-workspace-only",
			subject: "ao-1",
			seed: func(st *fakeStore) {
				rec := domain.SessionRecord{
					ID: "ao-1", ProjectID: "ao", Kind: domain.KindWorker,
					SchedulingState: domain.SchedulingAdmitted, ProvisionState: domain.SessionProvisionReady,
					AdmittedAt: &at, CreatedAt: at, UpdatedAt: at,
				}
				rec.Metadata.WorkspacePath = "/tmp/p5-ws"
				st.sessions["ao-1"] = rec
			},
			wantLaunched: 0,
			wantState:    domain.SchedulingImmediate,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakeStore()
			seedProject(st, "ao", domain.SchedulingConfig{MaxActiveSessions: 1})
			tc.seed(st)
			launcher := &fakeLauncher{}
			sched := newScheduler(st, launcher, tc.facts)

			if err := sched.Recover(context.Background()); err != nil {
				t.Fatalf("first recover: %v", err)
			}
			if len(launcher.launched) != tc.wantLaunched {
				t.Fatalf("launched = %d, want %d", len(launcher.launched), tc.wantLaunched)
			}
			rec := st.sessions[tc.subject]
			if rec.SchedulingState != tc.wantState || domain.BlockedReason(rec.BlockedReason) != tc.wantReason {
				t.Fatalf("state=%s reason=%q, want %s/%q", rec.SchedulingState, rec.BlockedReason, tc.wantState, tc.wantReason)
			}

			before := len(launcher.launched)
			if err := sched.Recover(context.Background()); err != nil {
				t.Fatalf("second recover: %v", err)
			}
			duplicateLaunches := len(launcher.launched) - before
			if duplicateLaunches != 0 {
				t.Fatalf("second recover launched %d duplicate session(s): %v", duplicateLaunches, launcher.launched[before:])
			}
			t.Logf("status=%s reason=%s launched=%d duplicate_launches=%d", rec.SchedulingState, rec.BlockedReason, len(launcher.launched), duplicateLaunches)
		})
	}
}

func countP5Edges(fixture p5LoadFixture) int {
	total := 0
	for _, session := range fixture.Sessions {
		total += len(session.DependsOn)
	}
	return total
}
