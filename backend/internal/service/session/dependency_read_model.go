package session

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// attachDependencies fills the derived dependency read model on already-built
// sessions: incoming edges with their satisfaction, and the reverse lookup the
// detail panel shows. Both are computed from durable edges and the same Kanban
// derivation the board renders, so the read model cannot disagree with the
// scheduler's admission decision.
//
// It is a no-op for an empty page, and the edge reads are batched per page.
func (s *Service) attachDependencies(ctx context.Context, sessions []domain.Session) error {
	if len(sessions) == 0 {
		return nil
	}
	ids := make([]domain.SessionID, 0, len(sessions))
	for _, sess := range sessions {
		ids = append(ids, sess.ID)
	}
	edgesBySession, err := s.store.ListSessionDependenciesForSessions(ctx, ids)
	if err != nil {
		return fmt.Errorf("list session dependencies: %w", err)
	}
	dependentsBySession, err := s.store.ListDependentSessionIDsForSessions(ctx, ids)
	if err != nil {
		return fmt.Errorf("list session dependents: %w", err)
	}
	var dependencyIDs []domain.SessionID
	seen := make(map[domain.SessionID]bool)
	for _, edges := range edgesBySession {
		for _, edge := range edges {
			if seen[edge.DependsOnSessionID] {
				continue
			}
			seen[edge.DependsOnSessionID] = true
			dependencyIDs = append(dependencyIDs, edge.DependsOnSessionID)
		}
	}
	var snapshots map[domain.SessionID]domain.DependencySnapshot
	if len(dependencyIDs) > 0 {
		snapshots, err = s.DependencySnapshots(ctx, dependencyIDs)
		if err != nil {
			return err
		}
	}
	for i := range sessions {
		for _, edge := range edgesBySession[sessions[i].ID] { //nolint:gosec // G602 false positive: i is a range index over the same slice
			satisfied, _ := domain.DependencySatisfied(edge, snapshots[edge.DependsOnSessionID])
			sessions[i].DependsOn = append(sessions[i].DependsOn, domain.SessionDependencyStatus{ //nolint:gosec // G602 false positive: i is a range index over the same slice
				SessionID:       edge.DependsOnSessionID,
				RequiredState:   edge.RequiredState,
				Satisfied:       satisfied,
				DependencyState: dependencyReadState(snapshots[edge.DependsOnSessionID]),
			})
		}
		if dependents := dependentsBySession[sessions[i].ID]; len(dependents) > 0 { //nolint:gosec // G602 false positive: i is a range index over the same slice
			sessions[i].Dependents = append([]domain.SessionID(nil), dependents...) //nolint:gosec // G602 false positive: i is a range index over the same slice
		}
	}
	return nil
}

// dependencyReadState is the state a dependency edge renders: the pre-launch
// scheduling state while the dependency is queued or blocked, otherwise its
// derived Kanban column. A row that no longer exists renders no state and reads
// as unsatisfied.
func dependencyReadState(snapshot domain.DependencySnapshot) string {
	if !snapshot.Found {
		return ""
	}
	if snapshot.SchedulingState.IsPreLaunch() {
		return string(snapshot.SchedulingState.WithDefault())
	}
	return string(snapshot.KanbanColumn)
}
