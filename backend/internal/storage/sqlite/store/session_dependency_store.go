package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// CreateSessionWithDependencies inserts a reserved session row and its incoming
// dependency edges in one transaction. A scheduler-visible row must never exist
// with a partial edge set: that would admit it before its dependencies.
func (s *Store) CreateSessionWithDependencies(
	ctx context.Context,
	rec domain.SessionRecord,
	deps []domain.SessionDependency,
) (domain.SessionRecord, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var created domain.SessionRecord
	err := s.inTx(ctx, "create reserved session", func(q *gen.Queries) error {
		var err error
		created, _, err = s.createSessionWith(ctx, q, rec)
		if err != nil {
			return err
		}
		return insertSessionDependencies(ctx, q, created.ID, deps)
	})
	if err != nil {
		return domain.SessionRecord{}, err
	}
	return created, nil
}

func insertSessionDependencies(ctx context.Context, q *gen.Queries, sessionID domain.SessionID, deps []domain.SessionDependency) error {
	for _, dep := range deps {
		if err := q.InsertSessionDependency(ctx, gen.InsertSessionDependencyParams{
			SessionID:          sessionID,
			DependsOnSessionID: dep.DependsOnSessionID,
			RequiredState:      dep.RequiredState,
			CreatedAt:          dep.CreatedAt,
		}); err != nil {
			return fmt.Errorf("insert dependency %s -> %s: %w", sessionID, dep.DependsOnSessionID, err)
		}
	}
	return nil
}

// ListSessionDependencies returns the incoming edges of one session in creation
// order.
func (s *Store) ListSessionDependencies(ctx context.Context, id domain.SessionID) ([]domain.SessionDependency, error) {
	rows, err := s.qr.ListSessionDependencies(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list dependencies for %s: %w", id, err)
	}
	return dependencyRows(rows), nil
}

// ListSessionDependenciesForSessions is the batch form used by the list read
// model.
func (s *Store) ListSessionDependenciesForSessions(ctx context.Context, ids []domain.SessionID) (map[domain.SessionID][]domain.SessionDependency, error) {
	out := make(map[domain.SessionID][]domain.SessionDependency, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return nil, fmt.Errorf("encode dependency session ids: %w", err)
	}
	rows, err := s.qr.ListSessionDependenciesForSessions(ctx, string(raw))
	if err != nil {
		return nil, fmt.Errorf("list dependencies for sessions: %w", err)
	}
	for _, row := range rows {
		out[row.SessionID] = append(out[row.SessionID], domain.SessionDependency{
			SessionID:          row.SessionID,
			DependsOnSessionID: row.DependsOnSessionID,
			RequiredState:      row.RequiredState,
			CreatedAt:          row.CreatedAt,
		})
	}
	return out, nil
}

// ListDependentSessionIDs returns the sessions that depend on id.
func (s *Store) ListDependentSessionIDs(ctx context.Context, id domain.SessionID) ([]domain.SessionID, error) {
	rows, err := s.qr.ListDependentSessionIDs(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list dependents of %s: %w", id, err)
	}
	return rows, nil
}

// ListDependentSessionIDsForSessions is the batch reverse lookup.
func (s *Store) ListDependentSessionIDsForSessions(ctx context.Context, ids []domain.SessionID) (map[domain.SessionID][]domain.SessionID, error) {
	out := make(map[domain.SessionID][]domain.SessionID, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return nil, fmt.Errorf("encode dependent session ids: %w", err)
	}
	rows, err := s.qr.ListDependentSessionIDsForSessions(ctx, string(raw))
	if err != nil {
		return nil, fmt.Errorf("list dependents for sessions: %w", err)
	}
	for _, row := range rows {
		out[row.DependsOnSessionID] = append(out[row.DependsOnSessionID], row.SessionID)
	}
	return out, nil
}

// CountSessionDependencies reports how many incoming edges a session has.
func (s *Store) CountSessionDependencies(ctx context.Context, id domain.SessionID) (int64, error) {
	count, err := s.qr.CountSessionDependencies(ctx, id)
	if err != nil {
		return 0, fmt.Errorf("count dependencies for %s: %w", id, err)
	}
	return count, nil
}

// ListSchedulingCandidateIDs returns the project's queued and blocked sessions
// in FIFO order, excluding launch failures, which never retry automatically.
func (s *Store) ListSchedulingCandidateIDs(ctx context.Context, projectID domain.ProjectID) ([]domain.SessionID, error) {
	rows, err := s.qr.ListSchedulingCandidates(ctx, optionalProjectID(projectID))
	if err != nil {
		return nil, fmt.Errorf("list scheduling candidates for %s: %w", projectID, err)
	}
	return rows, nil
}

// ListSchedulingProjectIDs returns every project with at least one queued or
// blocked session.
func (s *Store) ListSchedulingProjectIDs(ctx context.Context) ([]domain.ProjectID, error) {
	rows, err := s.qr.ListSchedulingProjectIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list scheduling projects: %w", err)
	}
	out := make([]domain.ProjectID, 0, len(rows))
	for _, row := range rows {
		if row != nil {
			out = append(out, *row)
		}
	}
	return out, nil
}

// CountActiveProjectSessions counts sessions occupying a project scheduling
// slot: not terminated, not a hidden preparation, and immediate or admitted.
func (s *Store) CountActiveProjectSessions(ctx context.Context, projectID domain.ProjectID) (int64, error) {
	count, err := s.qr.CountActiveProjectSessions(ctx, optionalProjectID(projectID))
	if err != nil {
		return 0, fmt.Errorf("count active sessions for %s: %w", projectID, err)
	}
	return count, nil
}

// SetSessionSchedulingState is a compare-and-set scheduling transition.
func (s *Store) SetSessionSchedulingState(
	ctx context.Context,
	id domain.SessionID,
	expected domain.SchedulingState,
	next domain.SchedulingState,
	reason domain.BlockedReason,
	now time.Time,
) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.SetSessionSchedulingState(ctx, gen.SetSessionSchedulingStateParams{
		SchedulingState: next.WithDefault(),
		BlockedReason:   string(reason),
		UpdatedAt:       now,
		ID:              id,
		ExpectedState:   expected.WithDefault(),
	})
	if err != nil {
		return false, fmt.Errorf("set scheduling state for %s: %w", id, err)
	}
	return rows > 0, nil
}

// AdmitSession durably records admission before the launch runs, so a crash
// after the commit is recovered by the startup reconcile.
func (s *Store) AdmitSession(ctx context.Context, id domain.SessionID, at time.Time, override bool) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.AdmitSession(ctx, gen.AdmitSessionParams{
		AdmittedAt:        timeToNullTime(at),
		AdmissionOverride: override,
		UpdatedAt:         at,
		ID:                id,
	})
	if err != nil {
		return false, fmt.Errorf("admit session %s: %w", id, err)
	}
	return rows > 0, nil
}

// SetSessionAdmissionOverride records a forced launch without changing the
// scheduling state.
func (s *Store) SetSessionAdmissionOverride(ctx context.Context, id domain.SessionID, now time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.SetSessionAdmissionOverride(ctx, gen.SetSessionAdmissionOverrideParams{
		UpdatedAt: now,
		ID:        id,
	})
	if err != nil {
		return false, fmt.Errorf("record admission override for %s: %w", id, err)
	}
	return rows > 0, nil
}

// MarkSessionSchedulingLaunched turns an admitted row into an ordinary active
// session after a successful launch, finishing the provisioning the
// reservation started.
func (s *Store) MarkSessionSchedulingLaunched(ctx context.Context, id domain.SessionID, now time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.MarkSessionSchedulingLaunched(ctx, gen.MarkSessionSchedulingLaunchedParams{
		UpdatedAt: now,
		ID:        id,
	})
	if err != nil {
		return false, fmt.Errorf("mark session %s launched: %w", id, err)
	}
	return rows > 0, nil
}

// SetSessionLaunchFailed blocks an admitted session whose provisioning failed.
func (s *Store) SetSessionLaunchFailed(ctx context.Context, id domain.SessionID, provisionError string, now time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.SetSessionLaunchFailed(ctx, gen.SetSessionLaunchFailedParams{
		ProvisionError: provisionError,
		UpdatedAt:      now,
		ID:             id,
	})
	if err != nil {
		return false, fmt.Errorf("record launch failure for %s: %w", id, err)
	}
	return rows > 0, nil
}

// CancelQueuedSession cancels a queued or blocked session without workspace
// cleanup. It reports false when the row had already moved past pre-launch.
func (s *Store) CancelQueuedSession(ctx context.Context, id domain.SessionID, now time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.CancelQueuedSession(ctx, gen.CancelQueuedSessionParams{
		UpdatedAt: now,
		ID:        id,
	})
	if err != nil {
		return false, fmt.Errorf("cancel queued session %s: %w", id, err)
	}
	return rows > 0, nil
}

// SetSessionCompleted records the explicit completion fact. It is idempotent:
// the first timestamp wins.
func (s *Store) SetSessionCompleted(ctx context.Context, id domain.SessionID, now time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.SetSessionCompleted(ctx, gen.SetSessionCompletedParams{
		CompletedAt: sql.NullTime{Time: now, Valid: true},
		UpdatedAt:   now,
		ID:          id,
	})
	if err != nil {
		return false, fmt.Errorf("complete session %s: %w", id, err)
	}
	return rows > 0, nil
}

// SetSessionQueuedSpawnConfig persists the normalized spawn request a reserved
// session will launch with.
func (s *Store) SetSessionQueuedSpawnConfig(ctx context.Context, id domain.SessionID, raw string, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.qw.SetSessionQueuedSpawnConfig(ctx, gen.SetSessionQueuedSpawnConfigParams{
		QueuedSpawnConfig: raw,
		UpdatedAt:         now,
		ID:                id,
	}); err != nil {
		return fmt.Errorf("store queued spawn config for %s: %w", id, err)
	}
	return nil
}

// ResetSessionForLaunchRetry returns a launch-failed session to the queue for a
// user-initiated retry.
func (s *Store) ResetSessionForLaunchRetry(ctx context.Context, id domain.SessionID, now time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.ResetSessionForLaunchRetry(ctx, gen.ResetSessionForLaunchRetryParams{
		UpdatedAt: now,
		ID:        id,
	})
	if err != nil {
		return false, fmt.Errorf("reset session %s for retry: %w", id, err)
	}
	return rows > 0, nil
}

// ListAdmittedSessionsWithoutWorkspace returns admitted rows that were decided
// but never launched. Startup recovery relaunches them under the launch guard.
func (s *Store) ListAdmittedSessionsWithoutWorkspace(ctx context.Context, projectID domain.ProjectID) ([]domain.SessionID, error) {
	rows, err := s.qr.ListAdmittedSessionsWithoutWorkspace(ctx, optionalProjectID(projectID))
	if err != nil {
		return nil, fmt.Errorf("list unlaunched admitted sessions for %s: %w", projectID, err)
	}
	return rows, nil
}

// ListAdmittedSessionsWithWorkspace returns admitted rows that already launched
// but never committed the transition to an ordinary active session. Recovery
// finishes that transition instead of launching them a second time.
func (s *Store) ListAdmittedSessionsWithWorkspace(ctx context.Context, projectID domain.ProjectID) ([]domain.SessionID, error) {
	rows, err := s.qr.ListAdmittedSessionsWithWorkspace(ctx, optionalProjectID(projectID))
	if err != nil {
		return nil, fmt.Errorf("list launched admitted sessions for %s: %w", projectID, err)
	}
	return rows, nil
}

func dependencyRows(rows []gen.SessionDependency) []domain.SessionDependency {
	out := make([]domain.SessionDependency, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.SessionDependency{
			SessionID:          row.SessionID,
			DependsOnSessionID: row.DependsOnSessionID,
			RequiredState:      row.RequiredState,
			CreatedAt:          row.CreatedAt,
		})
	}
	return out
}
