package scheduling

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

// decide is the pure scheduling policy for one session: what state should a
// pre-launch candidate hold after its edges are evaluated?
//
// A hard dependency failure wins over a pending one, so a session waiting on
// one dead dependency reports the reason the user must act on rather than the
// reason that may still resolve itself.
func decide(edges []domain.SessionDependency, snapshots map[domain.SessionID]domain.DependencySnapshot) (domain.SchedulingState, domain.BlockedReason) {
	var pending domain.BlockedReason
	for _, edge := range edges {
		ok, reason := edgeSatisfied(edge, snapshots[edge.DependsOnSessionID])
		if ok {
			continue
		}
		if reason.IsDependencyFailure() {
			return domain.SchedulingBlocked, reason
		}
		if pending == "" {
			pending = reason
		}
	}
	if pending != "" {
		return domain.SchedulingBlocked, pending
	}
	return domain.SchedulingQueued, ""
}

// edgeSatisfied evaluates one edge against the dependency's derived facts.
func edgeSatisfied(edge domain.SessionDependency, dep domain.DependencySnapshot) (bool, domain.BlockedReason) {
	if !dep.Found {
		return false, domain.BlockedDependencyMissing
	}
	if dep.SchedulingState.WithDefault() == domain.SchedulingCancelled {
		return false, domain.BlockedDependencyCancelled
	}
	if dep.IsTerminated && !dep.Succeeded() {
		return false, domain.BlockedDependencyFailed
	}
	// A dependency that has not launched yet cannot have reached any required
	// state; that is pending work, not a failure.
	if dep.SchedulingState.IsPreLaunch() {
		return false, domain.BlockedDependencyPending
	}
	switch edge.RequiredState {
	case domain.DependencyReady:
		if dep.KanbanColumn == domain.KanbanReady {
			return true, ""
		}
		return false, domain.BlockedDependencyPending
	case domain.DependencyMerged:
		if dep.PRCount == 0 && !dep.OutputType.HasPR() {
			// The dependency can no longer open a PR, or never could; waiting
			// will never satisfy this edge.
			return false, domain.BlockedDependencyUnsatisfiable
		}
		if dep.PRCount > 0 && dep.AllPRsMerged {
			return true, ""
		}
		return false, domain.BlockedDependencyPending
	case domain.DependencyCompleted:
		if dep.CompletedAt != nil {
			return true, ""
		}
		return false, domain.BlockedDependencyPending
	default:
		return false, domain.BlockedInvalid
	}
}
