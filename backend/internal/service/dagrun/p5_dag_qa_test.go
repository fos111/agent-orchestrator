package dagrun

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestP5Dag64NodesSubmitScheduleComplete loads the phase-5 DAG fixture and
// drives one submit through the real run service. It proves the atomic create
// path produces one session per node, the initial queue reflects the project
// limit and dependency edges, and a completed dependency snapshot moves the
// whole run to completed without duplicate sessions.
func TestP5Dag64NodesSubmitScheduleComplete(t *testing.T) {
	raw, err := os.ReadFile("../scheduling/testdata/load/dag_64_nodes.json")
	if err != nil {
		t.Fatalf("read dag fixture: %v", err)
	}
	var spec domain.DagRunSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("decode dag fixture: %v", err)
	}

	project := testProject()
	project.Config.Scheduling.MaxActiveSessions = 1
	project.Config.Scheduling.MaxDagNodesPerRun = 64
	store := newFakeStore(project)
	waker := &fakeWaker{}
	svc := testService(store, fakePlanner{}, fakeSnapshots{}, waker, &fakeTerminator{})

	view, err := svc.Submit(context.Background(), "p", spec)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if store.createCalls != 1 {
		t.Fatalf("create calls = %d, want 1", store.createCalls)
	}
	nodes := store.nodes["dag-1"]
	if len(nodes) != len(spec.Nodes) {
		t.Fatalf("created sessions = %d, want %d", len(nodes), len(spec.Nodes))
	}
	unique := map[domain.SessionID]struct{}{}
	for _, node := range nodes {
		unique[node.ID] = struct{}{}
	}
	duplicateSessions := len(nodes) - len(unique)
	if duplicateSessions != 0 {
		t.Fatalf("duplicate sessions = %d, want 0", duplicateSessions)
	}
	edges := 0
	for _, deps := range store.edges {
		edges += len(deps)
	}
	expectedEdges := 0
	roots := 0
	for _, node := range spec.Nodes {
		expectedEdges += len(node.DependsOn)
		if len(node.DependsOn) == 0 {
			roots++
		}
	}
	if edges != expectedEdges {
		t.Fatalf("persisted edges = %d, want %d", edges, expectedEdges)
	}

	blockedLimit, blockedDependency := 0, 0
	for _, node := range view.Nodes {
		switch domain.BlockedReason(node.BlockedReason) {
		case domain.BlockedProjectLimit:
			blockedLimit++
		case domain.BlockedDependencyPending:
			blockedDependency++
		}
	}
	if view.Status != domain.DagRunPending {
		t.Fatalf("status before completion = %s, want pending", view.Status)
	}
	if view.Counts.Queued != 1 || view.Counts.Blocked != 63 || view.Counts.Total != 64 {
		t.Fatalf("counts before completion = %+v, want queued=1 blocked=63 total=64", view.Counts)
	}
	if blockedLimit != 7 || blockedDependency != 56 {
		t.Fatalf("blocked limit=%d dependency=%d, want 7/56", blockedLimit, blockedDependency)
	}
	if len(waker.projects) != 1 || waker.projects[0] != "p" {
		t.Fatalf("waker = %v, want [p]", waker.projects)
	}

	for i := range nodes {
		nodes[i].SchedulingState = domain.SchedulingImmediate
		nodes[i].ProvisionState = domain.SessionProvisionReady
		nodes[i].BlockedReason = ""
		nodes[i].IsTerminated = false
	}
	store.nodes["dag-1"] = nodes
	snapshots := fakeSnapshots{byID: map[domain.SessionID]domain.DependencySnapshot{}}
	for _, node := range nodes {
		snapshots.byID[node.ID] = domain.DependencySnapshot{
			Found:           true,
			SchedulingState: domain.SchedulingImmediate,
			KanbanColumn:    domain.KanbanReady,
			OutputType:      domain.SessionOutputPR,
			PRCount:         1,
			AllPRsMerged:    true,
		}
	}
	svc = testService(store, fakePlanner{}, snapshots, waker, nil)
	completed, err := svc.Get(context.Background(), "dag-1")
	if err != nil {
		t.Fatalf("get completed run: %v", err)
	}
	if completed.Status != domain.DagRunCompleted {
		t.Fatalf("status after completion = %s, want completed", completed.Status)
	}
	if completed.Counts.Completed != 64 || completed.Counts.Total != 64 {
		t.Fatalf("counts after completion = %+v, want completed=64 total=64", completed.Counts)
	}

	t.Logf("nodes=%d edges=%d roots=%d dependents=%d queued_before=%d blocked_limit=%d blocked_dependency=%d unique_sessions=%d create_calls=%d status_before=%s status_after=%s completed=%d total=%d duplicate_sessions=%d",
		len(spec.Nodes), edges, roots, len(spec.Nodes)-roots, view.Counts.Queued, blockedLimit, blockedDependency,
		len(unique), store.createCalls, view.Status, completed.Status, completed.Counts.Completed, completed.Counts.Total, duplicateSessions)
}
