package domain

import "time"

// SchedulingState answers whether a session may launch. It is deliberately
// separate from delivery status and the Kanban column: a session can be blocked
// while its delivery state is still building. Rows written before dependency
// scheduling existed read back as immediate.
type SchedulingState string

// Scheduling states.
const (
	// SchedulingImmediate is ordinary spawn behavior: no dependency scheduling.
	SchedulingImmediate SchedulingState = "immediate"
	// SchedulingQueued means every dependency is satisfied and the session waits
	// for the scheduler to admit it.
	SchedulingQueued SchedulingState = "queued"
	// SchedulingBlocked means the session cannot be admitted yet. BlockedReason
	// explains why; the session is re-evaluated when facts change.
	SchedulingBlocked SchedulingState = "blocked"
	// SchedulingAdmitted means the scheduler authorized launch. Provisioning or
	// running follows.
	SchedulingAdmitted SchedulingState = "admitted"
	// SchedulingCancelled means the session was cancelled before launch.
	SchedulingCancelled SchedulingState = "cancelled"
)

// WithDefault resolves the zero value to immediate for rows written before this
// column existed.
func (s SchedulingState) WithDefault() SchedulingState {
	if s == "" {
		return SchedulingImmediate
	}
	return s
}

// IsPreLaunch reports whether the session owns no workspace and no agent yet.
func (s SchedulingState) IsPreLaunch() bool {
	switch s.WithDefault() {
	case SchedulingQueued, SchedulingBlocked:
		return true
	default:
		return false
	}
}

// CountsAsActive reports whether an admitted session occupies a project slot.
func (s SchedulingState) CountsAsActive() bool {
	switch s.WithDefault() {
	case SchedulingImmediate, SchedulingAdmitted:
		return true
	default:
		return false
	}
}

// BlockedReason explains why a session is blocked. Empty means not blocked.
type BlockedReason string

// Blocked reasons. The dependency_* family names a dependency fact; the limit
// family names a scheduling resource.
const (
	BlockedDependencyPending       BlockedReason = "dependency_pending"
	BlockedDependencyFailed        BlockedReason = "dependency_failed"
	BlockedDependencyCancelled     BlockedReason = "dependency_cancelled"
	BlockedDependencyMissing       BlockedReason = "dependency_missing"
	BlockedDependencyCycle         BlockedReason = "dependency_cycle"
	BlockedDependencyUnsatisfiable BlockedReason = "dependency_unsatisfiable"
	BlockedProjectLimit            BlockedReason = "project_limit"
	BlockedTeamLimit               BlockedReason = "team_limit"
	BlockedLaunchFailed            BlockedReason = "launch_failed"
	BlockedInvalid                 BlockedReason = "invalid"
)

// IsDependencyFailure reports whether the reason describes a dependency that can
// never satisfy the edge. Such a session is not retried automatically.
func (r BlockedReason) IsDependencyFailure() bool {
	switch r {
	case BlockedDependencyFailed, BlockedDependencyCancelled, BlockedDependencyMissing,
		BlockedDependencyCycle, BlockedDependencyUnsatisfiable:
		return true
	default:
		return false
	}
}

// LaunchFailed reports whether the reason is a failed admission launch, which
// the automatic reconciler deliberately skips until a user retries.
func (r BlockedReason) LaunchFailed() bool { return r == BlockedLaunchFailed }

// DependencyRequiredState is the state a dependency must reach before its
// dependent may be admitted.
type DependencyRequiredState string

// Dependency required states.
const (
	// DependencyReady is satisfied when the dependency's Kanban column is ready.
	// This includes merged, closed-without-merge, approved, and mergeable PRs,
	// matching the board's existing ready semantics.
	DependencyReady DependencyRequiredState = "ready"
	// DependencyMerged requires at least one attributed PR and every attributed
	// PR merged.
	DependencyMerged DependencyRequiredState = "merged"
	// DependencyCompleted requires an explicit completed_at fact.
	DependencyCompleted DependencyRequiredState = "completed"
)

// Valid reports whether the state is one of the known required states.
func (s DependencyRequiredState) Valid() bool {
	switch s {
	case DependencyReady, DependencyMerged, DependencyCompleted:
		return true
	default:
		return false
	}
}

// DefaultDependencyRequiredState infers the edge state from a dependency's
// output type when the caller did not name one: PR-shaped output defaults to
// merged, artifact and no-output sessions default to completed.
func DefaultDependencyRequiredState(output SessionOutputType) DependencyRequiredState {
	if output.HasPR() {
		return DependencyMerged
	}
	return DependencyCompleted
}

// SessionDependency is one durable edge: SessionID may not be admitted until
// DependsOnSessionID reaches RequiredState.
type SessionDependency struct {
	SessionID          SessionID               `json:"sessionId"`
	DependsOnSessionID SessionID               `json:"dependsOnSessionId"`
	RequiredState      DependencyRequiredState `json:"requiredState" enum:"ready,merged,completed"`
	CreatedAt          time.Time               `json:"createdAt"`
}

// SessionDependencyStatus is the read-model rendering of one dependency edge.
// It is derived at read time: Satisfied and DependencyState are never stored.
type SessionDependencyStatus struct {
	SessionID       SessionID               `json:"sessionId"`
	RequiredState   DependencyRequiredState `json:"requiredState" enum:"ready,merged,completed"`
	Satisfied       bool                    `json:"satisfied"`
	DependencyState string                  `json:"dependencyState,omitempty"`
}

// SessionDependencySpec is one dependency a caller asks to create. Empty
// RequiredState means the edge follows the dependency's output-type default.
type SessionDependencySpec struct {
	SessionID     SessionID
	RequiredState DependencyRequiredState
}

// DependencySnapshot is the derived delivery facts the scheduler needs to
// evaluate one edge. Nothing in it is stored: the Kanban column and the PR
// aggregate are computed at read time, so the queue follows the same delivery
// rules the board does instead of a second, divergent predicate.
type DependencySnapshot struct {
	// Found is false when the dependency row no longer exists.
	Found           bool
	SchedulingState SchedulingState
	IsTerminated    bool
	CompletedAt     *time.Time
	OutputType      SessionOutputType
	KanbanColumn    KanbanColumn
	// PRCount counts the dependency's attributed PRs, and AllPRsMerged says
	// every one of them is merged. PRCount == 0 with AllPRsMerged false is the
	// "no PR yet" case, not a vacuously merged one.
	PRCount      int
	AllPRsMerged bool
}

// Succeeded reports whether a terminated dependency reached a success fact.
// A termination without one is a failure, not a satisfied edge.
func (s DependencySnapshot) Succeeded() bool {
	if s.CompletedAt != nil {
		return true
	}
	if s.KanbanColumn == KanbanReady {
		return true
	}
	return s.PRCount > 0 && s.AllPRsMerged
}
