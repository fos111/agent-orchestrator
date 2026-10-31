package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func sampleDagRun(id, project, name string) domain.DagRun {
	return domain.DagRun{
		ID:        domain.DagRunID(id),
		ProjectID: domain.ProjectID(project),
		Name:      name,
		Status:    domain.DagRunPending,
		CreatedAt: time.Now().UTC().Truncate(time.Second),
	}
}

func TestDagRunInsertSelectAndList(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "dag")

	older := sampleDagRun("run-older", "dag", "older")
	if err := s.InsertDagRun(ctx, older); err != nil {
		t.Fatalf("insert older run: %v", err)
	}
	newer := sampleDagRun("run-newer", "dag", "newer")
	newer.CreatedAt = older.CreatedAt.Add(time.Minute)
	if err := s.InsertDagRun(ctx, newer); err != nil {
		t.Fatalf("insert newer run: %v", err)
	}

	got, ok, err := s.SelectDagRunByID(ctx, older.ID)
	if err != nil || !ok {
		t.Fatalf("select run: ok=%v err=%v", ok, err)
	}
	if got.Name != older.Name || got.Status != domain.DagRunPending || got.ProjectID != older.ProjectID {
		t.Fatalf("round-trip mismatch: got %+v want %+v", got, older)
	}
	if got.RootSessionID != nil || got.CancelRequestedAt != nil || got.CompletedAt != nil {
		t.Fatalf("new run has unexpected optional fields: %+v", got)
	}
	if !got.CreatedAt.Equal(older.CreatedAt) {
		t.Fatalf("createdAt = %v, want %v", got.CreatedAt, older.CreatedAt)
	}

	if _, ok, err := s.SelectDagRunByID(ctx, "run-missing"); err != nil || ok {
		t.Fatalf("select missing run: ok=%v err=%v", ok, err)
	}

	runs, err := s.SelectDagRunsByProject(ctx, "dag")
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 2 || runs[0].ID != "run-newer" || runs[1].ID != "run-older" {
		t.Fatalf("runs = %+v, want newest first", runs)
	}
}

func TestDagRunStatusAndCancellationAreMonotonic(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "dag")
	run := sampleDagRun("run-1", "dag", "feature-x")
	if err := s.InsertDagRun(ctx, run); err != nil {
		t.Fatalf("insert run: %v", err)
	}

	changed, err := s.UpdateDagRunStatus(ctx, run.ID, domain.DagRunRunning, nil)
	if err != nil || !changed {
		t.Fatalf("pending -> running: changed=%v err=%v", changed, err)
	}

	cancelledAt := run.CreatedAt.Add(time.Minute)
	changed, err = s.CancelDagRun(ctx, run.ID, cancelledAt)
	if err != nil || !changed {
		t.Fatalf("cancel running run: changed=%v err=%v", changed, err)
	}
	got, ok, err := s.SelectDagRunByID(ctx, run.ID)
	if err != nil || !ok {
		t.Fatalf("select cancelled run: ok=%v err=%v", ok, err)
	}
	if got.Status != domain.DagRunCancelled || got.CancelRequestedAt == nil || got.CompletedAt == nil {
		t.Fatalf("cancelled run = %+v", got)
	}
	if !got.CancelRequestedAt.Equal(cancelledAt) || !got.CompletedAt.Equal(cancelledAt) {
		t.Fatalf("cancel timestamps = %v / %v, want %v", got.CancelRequestedAt, got.CompletedAt, cancelledAt)
	}

	// A cancelled run never moves again, and cancelling twice is a no-op the
	// caller can treat as already cancelled.
	if changed, err := s.UpdateDagRunStatus(ctx, run.ID, domain.DagRunCompleted, &cancelledAt); err != nil || changed {
		t.Fatalf("update cancelled run: changed=%v err=%v", changed, err)
	}
	if changed, err := s.CancelDagRun(ctx, run.ID, cancelledAt); err != nil || changed {
		t.Fatalf("cancel cancelled run: changed=%v err=%v", changed, err)
	}
}

// dagSeed builds one node with the scheduling state the DAG service assigns
// before persisting.
func dagSeed(project string, nodeID domain.DagNodeID, state domain.SchedulingState, deps ...domain.DagNodeDependencySeed) domain.DagNodeSeed {
	rec := sampleRecord(project)
	rec.SchedulingState = state
	return domain.DagNodeSeed{NodeID: nodeID, Record: rec, Dependencies: deps}
}

func TestCreateDagRunWithNodesPersistsWholeGraph(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "dag")
	run := sampleDagRun("run-1", "dag", "feature-x")
	nodes := []domain.DagNodeSeed{
		dagSeed("dag", "build", domain.SchedulingQueued),
		dagSeed("dag", "test", domain.SchedulingBlocked,
			domain.DagNodeDependencySeed{NodeID: "build", RequiredState: domain.DependencyMerged}),
	}

	createdRun, created, err := s.CreateDagRunWithNodes(ctx, run, nodes)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if createdRun.ID != run.ID || len(created) != 2 {
		t.Fatalf("created run=%+v nodes=%d", createdRun, len(created))
	}
	for i, rec := range created {
		if rec.DagRunID == nil || *rec.DagRunID != run.ID {
			t.Fatalf("node %d run id = %v", i, rec.DagRunID)
		}
		if rec.DagNodeID == nil || *rec.DagNodeID != nodes[i].NodeID {
			t.Fatalf("node %d node id = %v", i, rec.DagNodeID)
		}
	}

	nodesByRun, err := s.ListSessionsByDagRun(ctx, run.ID)
	if err != nil || len(nodesByRun) != 2 {
		t.Fatalf("list nodes: n=%d err=%v", len(nodesByRun), err)
	}
	if *nodesByRun[0].DagNodeID != "build" || *nodesByRun[1].DagNodeID != "test" {
		t.Fatalf("node order = %v, %v", *nodesByRun[0].DagNodeID, *nodesByRun[1].DagNodeID)
	}
	edges, err := s.ListSessionDependenciesForSessions(ctx, []domain.SessionID{created[1].ID})
	if err != nil {
		t.Fatalf("list edges: %v", err)
	}
	if len(edges[created[1].ID]) != 1 || edges[created[1].ID][0].DependsOnSessionID != created[0].ID {
		t.Fatalf("edges = %+v", edges)
	}
	batch, err := s.ListSessionsByDagRunIDs(ctx, []domain.DagRunID{run.ID})
	if err != nil || len(batch[run.ID]) != 2 {
		t.Fatalf("batch list: n=%d err=%v", len(batch[run.ID]), err)
	}
	if count, err := s.CountDagRunNodes(ctx, run.ID); err != nil || count != 2 {
		t.Fatalf("count nodes: n=%d err=%v", count, err)
	}
}

func TestCreateDagRunWithNodesRollsBackEverything(t *testing.T) {
	for _, tc := range []struct {
		name  string
		nodes func() []domain.DagNodeSeed
	}{
		{
			name: "unknown dependency",
			nodes: func() []domain.DagNodeSeed {
				return []domain.DagNodeSeed{
					dagSeed("dag", "build", domain.SchedulingQueued),
					dagSeed("dag", "test", domain.SchedulingBlocked,
						domain.DagNodeDependencySeed{NodeID: "ghost", RequiredState: domain.DependencyCompleted}),
				}
			},
		},
		{
			name: "duplicate node id",
			nodes: func() []domain.DagNodeSeed {
				return []domain.DagNodeSeed{
					dagSeed("dag", "build", domain.SchedulingQueued),
					dagSeed("dag", "build", domain.SchedulingQueued),
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			ctx := context.Background()
			seedProject(t, s, "dag")
			run := sampleDagRun("run-1", "dag", "feature-x")

			if _, _, err := s.CreateDagRunWithNodes(ctx, run, tc.nodes()); err == nil {
				t.Fatal("invalid graph was persisted")
			}
			if _, ok, err := s.SelectDagRunByID(ctx, run.ID); err != nil || ok {
				t.Fatalf("run survived rollback: ok=%v err=%v", ok, err)
			}
			if nodes, err := s.ListSessionsByDagRun(ctx, run.ID); err != nil || len(nodes) != 0 {
				t.Fatalf("nodes survived rollback: n=%d err=%v", len(nodes), err)
			}
			// The session number sequence must roll back with the rows, so the
			// next valid submission still starts at project-1.
			valid := []domain.DagNodeSeed{dagSeed("dag", "solo", domain.SchedulingQueued)}
			_, created, err := s.CreateDagRunWithNodes(ctx, sampleDagRun("run-2", "dag", "retry"), valid)
			if err != nil {
				t.Fatalf("create after rollback: %v", err)
			}
			if created[0].ID != "dag-1" {
				t.Fatalf("session id after rollback = %s, want dag-1", created[0].ID)
			}
		})
	}
}

func TestCancelDagRunNodesLeavesAdmittedNodes(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "dag")
	run := sampleDagRun("run-1", "dag", "feature-x")
	nodes := []domain.DagNodeSeed{
		dagSeed("dag", "queued", domain.SchedulingQueued),
		dagSeed("dag", "blocked", domain.SchedulingBlocked),
		dagSeed("dag", "running", domain.SchedulingAdmitted),
	}
	_, created, err := s.CreateDagRunWithNodes(ctx, run, nodes)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	cancelled, err := s.CancelDagRunNodes(ctx, run.ID, run.CreatedAt.Add(time.Minute))
	if err != nil || cancelled != 2 {
		t.Fatalf("cancel nodes: n=%d err=%v", cancelled, err)
	}
	for i, want := range []domain.SchedulingState{domain.SchedulingCancelled, domain.SchedulingCancelled, domain.SchedulingAdmitted} {
		got, ok, err := s.GetSession(ctx, created[i].ID)
		if err != nil || !ok {
			t.Fatalf("get node %d: ok=%v err=%v", i, ok, err)
		}
		if got.SchedulingState.WithDefault() != want {
			t.Fatalf("node %d state = %s, want %s", i, got.SchedulingState, want)
		}
		if i < 2 && !got.IsTerminated {
			t.Fatalf("cancelled node %d is not terminated", i)
		}
		if i == 2 && got.IsTerminated {
			t.Fatal("admitted node was terminated by the node cancel")
		}
	}
}

func TestDagRunRejectsUnknownProject(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	run := sampleDagRun("run-1", "missing", "orphan")
	if err := s.InsertDagRun(ctx, run); err == nil {
		t.Fatal("insert run for an unregistered project succeeded")
	}
}
