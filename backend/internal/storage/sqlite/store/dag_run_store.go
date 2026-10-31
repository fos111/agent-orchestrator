package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// InsertDagRun persists one run row. Node creation is a separate transaction
// owned by the DAG service: the run and all of its nodes are created together
// or not at all.
func (s *Store) InsertDagRun(ctx context.Context, run domain.DagRun) error {
	_, err := s.qw.InsertDagRun(ctx, gen.InsertDagRunParams{
		ID:                run.ID,
		ProjectID:         run.ProjectID,
		RootSessionID:     run.RootSessionID,
		Name:              run.Name,
		Status:            run.Status,
		CancelRequestedAt: nullTimeValue(run.CancelRequestedAt),
		CreatedAt:         run.CreatedAt,
		CompletedAt:       nullTimeValue(run.CompletedAt),
	})
	if err != nil {
		return fmt.Errorf("insert dag run %s: %w", run.ID, err)
	}
	return nil
}

// SelectDagRunByID looks up one run, reporting whether it existed so callers
// can answer 404 for an unknown id.
func (s *Store) SelectDagRunByID(ctx context.Context, id domain.DagRunID) (domain.DagRun, bool, error) {
	row, err := s.qr.SelectDagRunByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DagRun{}, false, nil
	}
	if err != nil {
		return domain.DagRun{}, false, fmt.Errorf("select dag run %s: %w", id, err)
	}
	return dagRunFromGen(row), true, nil
}

// SelectDagRunsByProject returns one project's runs newest first.
func (s *Store) SelectDagRunsByProject(ctx context.Context, projectID domain.ProjectID) ([]domain.DagRun, error) {
	rows, err := s.qr.SelectDagRunsByProject(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("select dag runs for project %s: %w", projectID, err)
	}
	out := make([]domain.DagRun, 0, len(rows))
	for _, row := range rows {
		out = append(out, dagRunFromGen(row))
	}
	return out, nil
}

// UpdateDagRunStatus stores the derived status of a pending or running run.
// Terminal runs are immutable, so it reports whether a row changed.
func (s *Store) UpdateDagRunStatus(ctx context.Context, id domain.DagRunID, status domain.DagRunStatus, completedAt *time.Time) (bool, error) {
	changed, err := s.qw.UpdateDagRunStatus(ctx, gen.UpdateDagRunStatusParams{
		Status:      status,
		CompletedAt: nullTimeValue(completedAt),
		ID:          id,
	})
	if err != nil {
		return false, fmt.Errorf("update dag run %s status: %w", id, err)
	}
	return changed > 0, nil
}

// CancelDagRun records a user cancellation and marks the run cancelled in one
// statement. It reports whether a pending or running run changed; completed and
// already-cancelled runs are left alone.
func (s *Store) CancelDagRun(ctx context.Context, id domain.DagRunID, at time.Time) (bool, error) {
	changed, err := s.qw.CancelDagRun(ctx, gen.CancelDagRunParams{
		CancelRequestedAt: sql.NullTime{Time: at.UTC(), Valid: true},
		CompletedAt:       sql.NullTime{Time: at.UTC(), Valid: true},
		ID:                id,
	})
	if err != nil {
		return false, fmt.Errorf("cancel dag run %s: %w", id, err)
	}
	return changed > 0, nil
}

// CountDagRunNodes counts the session rows currently attributed to a run.
func (s *Store) CountDagRunNodes(ctx context.Context, id domain.DagRunID) (int64, error) {
	count, err := s.qr.CountDagRunNodes(ctx, &id)
	if err != nil {
		return 0, fmt.Errorf("count dag run %s nodes: %w", id, err)
	}
	return count, nil
}

func dagRunFromGen(row gen.DagRun) domain.DagRun {
	return domain.DagRun{
		ID:                row.ID,
		ProjectID:         row.ProjectID,
		RootSessionID:     row.RootSessionID,
		Name:              row.Name,
		Status:            row.Status,
		CancelRequestedAt: nullTimePointer(row.CancelRequestedAt),
		CreatedAt:         row.CreatedAt,
		CompletedAt:       nullTimePointer(row.CompletedAt),
	}
}

// nullTimeValue maps an optional timestamp to the sqlc nullable form. Zero
// times are treated as absent, matching the store's other optional timestamps.
func nullTimeValue(t *time.Time) sql.NullTime {
	if t == nil || t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t.UTC(), Valid: true}
}

func nullTimePointer(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	value := t.Time.UTC()
	return &value
}
