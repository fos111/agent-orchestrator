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

// edgeSatisfied evaluates one edge against the dependency's derived facts. The
// policy lives in domain so the session read model reports the same answer.
func edgeSatisfied(edge domain.SessionDependency, dep domain.DependencySnapshot) (bool, domain.BlockedReason) {
	return domain.DependencySatisfied(edge, dep)
}
