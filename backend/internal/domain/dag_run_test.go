package domain

import (
	"testing"
	"time"
)

func dagNode(nodeID DagNodeID, status DagNodeStatus, reason BlockedReason) DagNodeView {
	return DagNodeView{NodeID: nodeID, Status: status, BlockedReason: string(reason)}
}

func at(sec int64) *time.Time {
	t := time.Unix(sec, 0).UTC()
	return &t
}

func TestDeriveDagNodeStatus(t *testing.T) {
	base := SessionRecord{}
	now := time.Unix(100, 0).UTC()
	for _, tc := range []struct {
		name     string
		rec      SessionRecord
		snapshot DependencySnapshot
		want     DagNodeStatus
	}{
		{"cancelled beats everything", SessionRecord{SchedulingState: SchedulingCancelled, IsTerminated: true}, DependencySnapshot{CompletedAt: &now}, DagNodeCancelled},
		{"queued", SessionRecord{SchedulingState: SchedulingQueued}, DependencySnapshot{}, DagNodeQueued},
		{"blocked", SessionRecord{SchedulingState: SchedulingBlocked}, DependencySnapshot{}, DagNodeBlocked},
		{"admitted and alive", SessionRecord{SchedulingState: SchedulingAdmitted}, DependencySnapshot{}, DagNodeRunning},
		{"admitted with completedAt", SessionRecord{SchedulingState: SchedulingAdmitted}, DependencySnapshot{CompletedAt: &now}, DagNodeCompleted},
		{"admitted and ready", SessionRecord{SchedulingState: SchedulingAdmitted}, DependencySnapshot{KanbanColumn: KanbanReady}, DagNodeCompleted},
		{"admitted with merged PR", SessionRecord{SchedulingState: SchedulingAdmitted}, DependencySnapshot{PRCount: 1, AllPRsMerged: true}, DagNodeCompleted},
		{"admitted with open PR", SessionRecord{SchedulingState: SchedulingAdmitted}, DependencySnapshot{PRCount: 1}, DagNodeRunning},
		{"terminated without success", SessionRecord{SchedulingState: SchedulingAdmitted, IsTerminated: true}, DependencySnapshot{}, DagNodeFailed},
		{"immediate default is admitted", base, DependencySnapshot{}, DagNodeRunning},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := DeriveDagNodeStatus(tc.rec, tc.snapshot); got != tc.want {
				t.Fatalf("status = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestDeriveDagRunStatus(t *testing.T) {
	all := func(statuses ...DagNodeStatus) []DagNodeView {
		out := make([]DagNodeView, 0, len(statuses))
		for i, status := range statuses {
			out = append(out, dagNode(DagNodeID(string(rune('a'+i))), status, ""))
		}
		return out
	}
	for _, tc := range []struct {
		name  string
		run   DagRun
		nodes []DagNodeView
		want  DagRunStatus
	}{
		{"cancel request wins", DagRun{CancelRequestedAt: at(10)}, all(DagNodeRunning), DagRunCancelled},
		{"all cancelled", DagRun{}, all(DagNodeCancelled, DagNodeCancelled), DagRunCancelled},
		{"no nodes", DagRun{}, nil, DagRunPending},
		{"all queued", DagRun{}, all(DagNodeQueued, DagNodeBlocked), DagRunPending},
		{"any admitted", DagRun{}, all(DagNodeQueued, DagNodeRunning), DagRunRunning},
		{"mixed completed and running", DagRun{}, all(DagNodeCompleted, DagNodeRunning), DagRunRunning},
		{"all completed", DagRun{}, all(DagNodeCompleted, DagNodeCompleted), DagRunCompleted},
		{"failed node with no admitted", DagRun{}, all(DagNodeFailed, DagNodeQueued), DagRunFailed},
		{"failed node alongside admitted work", DagRun{}, all(DagNodeFailed, DagNodeRunning), DagRunRunning},
		{
			"blocked by failed dependency with no admitted",
			DagRun{},
			[]DagNodeView{dagNode("a", DagNodeBlocked, BlockedDependencyFailed)},
			DagRunFailed,
		},
		{
			"blocked by dependency cycle",
			DagRun{},
			[]DagNodeView{dagNode("a", DagNodeBlocked, BlockedDependencyCycle)},
			DagRunFailed,
		},
		{
			"blocked by launch failure",
			DagRun{},
			[]DagNodeView{dagNode("a", DagNodeBlocked, BlockedLaunchFailed)},
			DagRunFailed,
		},
		{
			"pending dependency does not fail the run",
			DagRun{},
			[]DagNodeView{dagNode("a", DagNodeBlocked, BlockedDependencyPending)},
			DagRunPending,
		},
		{
			"project limit does not fail the run",
			DagRun{},
			[]DagNodeView{dagNode("a", DagNodeBlocked, BlockedProjectLimit)},
			DagRunPending,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := DeriveDagRunStatus(tc.run, tc.nodes); got != tc.want {
				t.Fatalf("status = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestCountDagNodes(t *testing.T) {
	counts := CountDagNodes([]DagNodeView{
		dagNode("a", DagNodeQueued, ""),
		dagNode("b", DagNodeBlocked, ""),
		dagNode("c", DagNodeBlocked, ""),
		dagNode("d", DagNodeRunning, ""),
		dagNode("e", DagNodeCompleted, ""),
		dagNode("f", DagNodeFailed, ""),
		dagNode("g", DagNodeCancelled, ""),
	})
	want := DagRunCounts{Total: 7, Queued: 1, Blocked: 2, Running: 1, Completed: 1, Failed: 1, Cancelled: 1}
	if counts != want {
		t.Fatalf("counts = %+v, want %+v", counts, want)
	}
}

func TestNewDagRunViewCarriesDerivedStatusAndCounts(t *testing.T) {
	run := DagRun{ID: "dag-1", ProjectID: "p", Name: "x", Status: DagRunPending}
	view := NewDagRunView(run, []DagNodeView{
		dagNode("a", DagNodeRunning, ""),
		dagNode("b", DagNodeQueued, ""),
	})
	if view.Status != DagRunRunning || view.Counts.Total != 2 || view.Counts.Running != 1 || view.Counts.Queued != 1 {
		t.Fatalf("view = %+v", view)
	}
}
