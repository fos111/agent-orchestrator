package domain

import "time"

// DagRunID identifies one atomic DAG submission. A run groups worker sessions;
// it is not a session and cannot be messaged, killed, or delivered to.
type DagRunID string

// DagNodeID is the submitter's identifier for one node inside a run. It is
// unique per run and is persisted alongside the session row so a retry or a
// read can map a node back to its submission without a second table.
type DagNodeID string

// DagRunStatus is the durable, listable summary of a run. The API read path
// also derives the status from node facts, so a stale stored value cannot make
// the UI claim a run finished when it did not.
type DagRunStatus string

// DAG run statuses.
const (
	// DagRunPending means no node has been admitted yet and no node has
	// failed terminally; nodes are queued or blocked on in-run dependencies.
	DagRunPending DagRunStatus = "pending"
	// DagRunRunning means at least one node has been admitted.
	DagRunRunning DagRunStatus = "running"
	// DagRunCompleted means every node reached terminal success.
	DagRunCompleted DagRunStatus = "completed"
	// DagRunFailed means a node failed or is blocked by a failed, cancelled,
	// missing, unsatisfiable, invalid, or launch-failed dependency and no node
	// is admitted.
	DagRunFailed DagRunStatus = "failed"
	// DagRunCancelled means the user cancelled the run. Nodes that were active
	// at cancellation time keep running and do not clear this status.
	DagRunCancelled DagRunStatus = "cancelled"
)

// Valid reports whether s is a stored DAG run status.
func (s DagRunStatus) Valid() bool {
	switch s {
	case DagRunPending, DagRunRunning, DagRunCompleted, DagRunFailed, DagRunCancelled:
		return true
	default:
		return false
	}
}

// Terminal reports whether s is a final run status. Pending and running runs
// still change on their own.
func (s DagRunStatus) Terminal() bool {
	return s == DagRunCompleted || s == DagRunFailed || s == DagRunCancelled
}

// DagRequiredState is the state an in-run dependency must reach before a node
// may be admitted. Phase 3 keeps this vocabulary local to the DAG domain so DAG
// validation does not depend on the session dependency scheduler landing first;
// the values are the same wire strings as DependencyRequiredState and are
// converted when a node becomes an ordinary session dependency.
type DagRequiredState string

// DAG in-run dependency required states.
const (
	DagRequiredReady     DagRequiredState = "ready"
	DagRequiredMerged    DagRequiredState = "merged"
	DagRequiredCompleted DagRequiredState = "completed"
)

// Valid reports whether s is a supported required state.
func (s DagRequiredState) Valid() bool {
	return s == DagRequiredReady || s == DagRequiredMerged || s == DagRequiredCompleted
}

// DAG submission bounds. Names reuse the session display-name budget; the
// prompt budget matches the spawn prompt cap so any valid node prompt can be
// handed to the spawn path without truncation. The node cap bounds one
// submission transaction and keeps the graph readable.
const (
	// MaxDagRunNameLength bounds the user-visible run name.
	MaxDagRunNameLength = 100
	// MaxDagNodeIDLength bounds one submitted node identifier.
	MaxDagNodeIDLength = 64
	// MaxDagNodeNameLength bounds a node's display name.
	MaxDagNodeNameLength = 100
	// MaxDagNodePromptLength bounds a node prompt. It matches the spawn prompt
	// cap.
	MaxDagNodePromptLength = 16 << 10
	// MaxDagNodesPerRun bounds how many sessions one submission may create.
	// The bound keeps one submission transaction and the rendered graph small
	// enough to read; it matches the documented 64-node run.
	MaxDagNodesPerRun = 64
)

// DagRun is one atomic DAG submission. RootSessionID is optional: it points at
// the coordinator session that submitted the run when there is one, and is
// cleared rather than cascading a delete if that session disappears.
type DagRun struct {
	ID            DagRunID     `json:"id"`
	ProjectID     ProjectID    `json:"projectId"`
	RootSessionID *SessionID   `json:"rootSessionId,omitempty"`
	Name          string       `json:"name"`
	Status        DagRunStatus `json:"status" enum:"pending,running,completed,failed,cancelled"`
	// CancelRequestedAt records the user's cancellation even while active
	// nodes are still finishing, so status derivation cannot resurrect a
	// cancelled run.
	CancelRequestedAt *time.Time `json:"cancelRequestedAt,omitempty"`
	CreatedAt         time.Time  `json:"createdAt"`
	CompletedAt       *time.Time `json:"completedAt,omitempty"`
}

// Cancelled reports whether the user asked to cancel the run.
func (r DagRun) Cancelled() bool {
	return r.CancelRequestedAt != nil
}

// DagNodeSpec is one submitted node. A node becomes a parentless worker
// session; the run is its coordinator, not its parent session.
type DagNodeSpec struct {
	// ID is the submitter's node identifier, unique within the run.
	ID DagNodeID `json:"id"`
	// Name is the node's display name. Empty defaults to ID.
	Name string `json:"name,omitempty"`
	// Prompt is the task handed to the worker.
	Prompt string `json:"prompt"`
	// Harness is the requested agent. Empty inherits the project default.
	Harness AgentHarness `json:"harness,omitempty"`
	// DependsOn lists node IDs in the same run that must satisfy RequiredState
	// first.
	DependsOn []DagNodeID `json:"dependsOn,omitempty"`
	// RequiredState is the state every DependsOn entry must reach. Empty
	// defaults from RequestedOutput: artifact nodes complete explicitly, all
	// other nodes wait for a merge.
	RequiredState DagRequiredState `json:"requiredState,omitempty" enum:"ready,merged,completed"`
	// RequestedOutput selects an artifact deliverable instead of a pull
	// request. A no-PR gate node uses RequestedOutputArtifact and is completed
	// explicitly.
	RequestedOutput RequestedOutput `json:"requestedOutput,omitempty"`
}

// DagRunSpec is a submitted run before any row exists.
type DagRunSpec struct {
	Name  string        `json:"name"`
	Nodes []DagNodeSpec `json:"nodes"`
}

// DefaultDagRequiredState returns the required state a node uses when the
// submission leaves it empty. Artifact nodes complete explicitly; ordinary
// task nodes wait for their dependency's pull request to merge.
func DefaultDagRequiredState(output RequestedOutput) DagRequiredState {
	if output == RequestedOutputArtifact {
		return DagRequiredCompleted
	}
	return DagRequiredMerged
}

// DagNodeStatus is the derived state of one run node in the read model. It is
// never stored: the session row keeps the durable facts, and the same
// derivation the run status uses is computed at read time.
type DagNodeStatus string

// DAG node statuses.
const (
	// DagNodeQueued means the node may launch as soon as a slot frees.
	DagNodeQueued DagNodeStatus = "queued"
	// DagNodeBlocked means the node waits on a dependency or a limit. The
	// node keeps the blocking reason so a failed dependency can fail the run.
	DagNodeBlocked DagNodeStatus = "blocked"
	// DagNodeRunning means the node was admitted and has not reached a
	// terminal fact yet.
	DagNodeRunning DagNodeStatus = "running"
	// DagNodeCompleted means the node reached the run's success predicate.
	DagNodeCompleted DagNodeStatus = "completed"
	// DagNodeFailed means the node terminated without a success fact.
	DagNodeFailed DagNodeStatus = "failed"
	// DagNodeCancelled means the node was cancelled before it launched.
	DagNodeCancelled DagNodeStatus = "cancelled"
)

// DagNodeView is one node of a run in the read model. SessionID is assigned by
// the daemon at creation and maps the submitter's node ID to the real session.
type DagNodeView struct {
	NodeID        DagNodeID     `json:"id"`
	SessionID     SessionID     `json:"sessionId"`
	Name          string        `json:"name"`
	Status        DagNodeStatus `json:"status" enum:"queued,blocked,running,completed,failed,cancelled"`
	BlockedReason string        `json:"blockedReason,omitempty"`
	DependsOn     []DagNodeID   `json:"dependsOn,omitempty"`
	CreatedAt     time.Time     `json:"createdAt"`
}

// DagRunCounts summarizes one run's nodes by derived status. The header uses
// it without walking the node list.
type DagRunCounts struct {
	Total     int `json:"total"`
	Queued    int `json:"queued"`
	Blocked   int `json:"blocked"`
	Running   int `json:"running"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
	Cancelled int `json:"cancelled"`
}

// DagRunView is the read model of one run: the stored summary plus its derived
// status, node counts, and nodes. Status is derived from node facts so a stale
// stored value can never make the UI claim a run finished when it did not.
type DagRunView struct {
	ID                DagRunID      `json:"id"`
	ProjectID         ProjectID     `json:"projectId"`
	RootSessionID     *SessionID    `json:"rootSessionId,omitempty"`
	Name              string        `json:"name"`
	Status            DagRunStatus  `json:"status" enum:"pending,running,completed,failed,cancelled"`
	CancelRequestedAt *time.Time    `json:"cancelRequestedAt,omitempty"`
	CreatedAt         time.Time     `json:"createdAt"`
	CompletedAt       *time.Time    `json:"completedAt,omitempty"`
	Counts            DagRunCounts  `json:"counts"`
	Nodes             []DagNodeView `json:"nodes"`
}

// DagNodeDependencySeed is one submitted edge, named by the submitter's node
// IDs. The store resolves both ends to created session IDs inside the
// submission transaction.
type DagNodeDependencySeed struct {
	NodeID        DagNodeID
	RequiredState DependencyRequiredState
}

// DagNodeSeed is one node ready to persist. The session Record already carries
// its scheduling state and queue config; the store fills the run and node ids
// and resolves Dependencies as it creates the rows.
type DagNodeSeed struct {
	NodeID       DagNodeID
	Record       SessionRecord
	Dependencies []DagNodeDependencySeed
}

// DeriveDagNodeStatus derives one node's status from its durable session row
// and the same delivery facts the dependency scheduler uses.
func DeriveDagNodeStatus(rec SessionRecord, snapshot DependencySnapshot) DagNodeStatus {
	if rec.SchedulingState.WithDefault() == SchedulingCancelled {
		return DagNodeCancelled
	}
	if rec.SchedulingState.IsPreLaunch() {
		if rec.SchedulingState.WithDefault() == SchedulingQueued {
			return DagNodeQueued
		}
		return DagNodeBlocked
	}
	if snapshot.Succeeded() {
		return DagNodeCompleted
	}
	if rec.IsTerminated {
		return DagNodeFailed
	}
	return DagNodeRunning
}

// DeriveDagRunStatus applies the documented run status precedence:
// cancel_requested, all cancelled, failed with no admitted node, all completed,
// any admitted, pending.
func DeriveDagRunStatus(run DagRun, nodes []DagNodeView) DagRunStatus {
	if run.CancelRequestedAt != nil || run.Status == DagRunCancelled {
		return DagRunCancelled
	}
	if len(nodes) == 0 {
		return DagRunPending
	}
	allCancelled := true
	allCompleted := true
	anyAdmitted := false
	anyFailed := false
	for _, node := range nodes {
		if node.Status != DagNodeCancelled {
			allCancelled = false
		}
		if node.Status != DagNodeCompleted {
			allCompleted = false
		}
		switch node.Status {
		case DagNodeRunning, DagNodeCompleted:
			anyAdmitted = true
		case DagNodeFailed:
			anyFailed = true
		case DagNodeBlocked:
			reason := BlockedReason(node.BlockedReason)
			if reason.IsDependencyFailure() || reason.LaunchFailed() {
				anyFailed = true
			}
		}
	}
	if allCancelled {
		return DagRunCancelled
	}
	if anyFailed && !anyAdmitted {
		return DagRunFailed
	}
	if allCompleted {
		return DagRunCompleted
	}
	if anyAdmitted {
		return DagRunRunning
	}
	return DagRunPending
}

// CountDagNodes summarizes node views.
func CountDagNodes(nodes []DagNodeView) DagRunCounts {
	counts := DagRunCounts{Total: len(nodes)}
	for _, node := range nodes {
		switch node.Status {
		case DagNodeQueued:
			counts.Queued++
		case DagNodeBlocked:
			counts.Blocked++
		case DagNodeRunning:
			counts.Running++
		case DagNodeCompleted:
			counts.Completed++
		case DagNodeFailed:
			counts.Failed++
		case DagNodeCancelled:
			counts.Cancelled++
		}
	}
	return counts
}

// NewDagRunView builds the read model from a stored run and its derived nodes.
func NewDagRunView(run DagRun, nodes []DagNodeView) DagRunView {
	status := DeriveDagRunStatus(run, nodes)
	return DagRunView{
		ID:                run.ID,
		ProjectID:         run.ProjectID,
		RootSessionID:     run.RootSessionID,
		Name:              run.Name,
		Status:            status,
		CancelRequestedAt: run.CancelRequestedAt,
		CreatedAt:         run.CreatedAt,
		CompletedAt:       run.CompletedAt,
		Counts:            CountDagNodes(nodes),
		Nodes:             nodes,
	}
}
