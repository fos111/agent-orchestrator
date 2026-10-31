package session

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// DependencySnapshots returns the derived delivery facts the dependency
// scheduler needs for the given sessions. It is the production implementation
// of the scheduler's narrow snapshot port: the scheduler owns the satisfaction
// policy, while this service owns the one Kanban derivation the board reads.
//
// It intentionally builds the presentation with the persisted OutputType and
// the batched PR/review reads, mirroring toSessionWithFacts. It does not list
// artifact directories or repair legacy rows: a dependency read must not write.
func (s *Service) DependencySnapshots(ctx context.Context, ids []domain.SessionID) (map[domain.SessionID]domain.DependencySnapshot, error) {
	out := make(map[domain.SessionID]domain.DependencySnapshot, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	recs := make([]domain.SessionRecord, 0, len(ids))
	for _, id := range ids {
		rec, found, err := s.store.GetSession(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("dependency snapshot %s: %w", id, err)
		}
		if !found {
			out[id] = domain.DependencySnapshot{}
			continue
		}
		recs = append(recs, rec)
	}
	if len(recs) == 0 {
		return out, nil
	}
	recIDs := make([]domain.SessionID, 0, len(recs))
	for _, rec := range recs {
		recIDs = append(recIDs, rec.ID)
	}
	prsBySession, err := s.store.ListPRFactsForSessions(ctx, recIDs)
	if err != nil {
		return nil, fmt.Errorf("dependency pr facts: %w", err)
	}
	runsBySession, err := s.store.ListCurrentHeadReviewRunsForSessions(ctx, recIDs)
	if err != nil {
		return nil, fmt.Errorf("dependency review runs: %w", err)
	}
	interrupted, err := s.store.ListInterruptedSessions(ctx, recIDs)
	if err != nil {
		return nil, fmt.Errorf("dependency interrupted sessions: %w", err)
	}
	now := s.now()
	for _, rec := range recs {
		prs := deduplicatePRFacts(prsBySession[rec.ID])
		runs := canonicalizeCurrentHeadReviewRuns(prs, runsBySession[rec.ID])
		presentation := deriveKanbanPresentation(rec, prs, runs, interrupted[rec.ID], now, s.harnessSignals(rec.Harness))
		allMerged := len(prs) > 0
		for _, pr := range prs {
			if !pr.Merged {
				allMerged = false
				break
			}
		}
		out[rec.ID] = domain.DependencySnapshot{
			Found:           true,
			SchedulingState: rec.SchedulingState.WithDefault(),
			IsTerminated:    rec.IsTerminated,
			CompletedAt:     rec.CompletedAt,
			OutputType:      rec.OutputType,
			KanbanColumn:    presentation.Column,
			PRCount:         len(prs),
			AllPRsMerged:    allMerged,
		}
	}
	return out, nil
}
