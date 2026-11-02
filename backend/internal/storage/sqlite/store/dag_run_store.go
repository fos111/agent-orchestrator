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

// CreateDagRunWithNodes persists one run, every node session, and every in-run
// dependency edge in one transaction. A submission is all-or-nothing: an
// invalid node index, a duplicate node id, or a failed insert rolls the whole
// run back, so a partial graph can never be scheduled.
//
// Nodes must arrive in dependency order (every dependency before its
// dependent); the service topologically sorts them before calling.
func (s *Store) CreateDagRunWithNodes(ctx context.Context, run domain.DagRun, nodes []domain.DagNodeSeed) (domain.DagRun, []domain.SessionRecord, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	created := make([]domain.SessionRecord, 0, len(nodes))
	err := s.inTx(ctx, "create dag run", func(q *gen.Queries) error {
		if _, err := q.InsertDagRun(ctx, gen.InsertDagRunParams{
			ID:                run.ID,
			ProjectID:         run.ProjectID,
			RootSessionID:     run.RootSessionID,
			Name:              run.Name,
			Status:            run.Status,
			CancelRequestedAt: nullTimeValue(run.CancelRequestedAt),
			CreatedAt:         run.CreatedAt,
			CompletedAt:       nullTimeValue(run.CompletedAt),
		}); err != nil {
			return fmt.Errorf("insert dag run %s: %w", run.ID, err)
		}
		sessionByNode := make(map[domain.DagNodeID]domain.SessionID, len(nodes))
		for _, node := range nodes {
			if node.NodeID == "" {
				return fmt.Errorf("dag run %s: node id is required", run.ID)
			}
			if _, duplicate := sessionByNode[node.NodeID]; duplicate {
				return fmt.Errorf("dag run %s: node id %s was inserted twice", run.ID, node.NodeID)
			}
			rec := node.Record
			runID := run.ID
			nodeID := node.NodeID
			rec.ProjectID = run.ProjectID
			rec.DagRunID = &runID
			rec.DagNodeID = &nodeID
			inserted, _, err := s.createSessionWith(ctx, q, rec)
			if err != nil {
				return err
			}
			sessionByNode[node.NodeID] = inserted.ID
			created = append(created, inserted)
		}
		for i, node := range nodes {
			for _, dep := range node.Dependencies {
				dependsOn, ok := sessionByNode[dep.NodeID]
				if !ok {
					return fmt.Errorf("dag run %s: node %s depends on unknown node %s", run.ID, node.NodeID, dep.NodeID)
				}
				if err := q.InsertSessionDependency(ctx, gen.InsertSessionDependencyParams{
					SessionID:          created[i].ID,
					DependsOnSessionID: dependsOn,
					RequiredState:      dep.RequiredState,
					CreatedAt:          created[i].CreatedAt,
				}); err != nil {
					return fmt.Errorf("insert dag dependency %s -> %s: %w", node.NodeID, dep.NodeID, err)
				}
			}
		}
		return nil
	})
	if err != nil {
		return domain.DagRun{}, nil, err
	}
	return run, created, nil
}

// ListSessionsByDagRun returns one run's node sessions in creation order.
func (s *Store) ListSessionsByDagRun(ctx context.Context, id domain.DagRunID) ([]domain.SessionRecord, error) {
	rows, err := s.qr.ListSessionsByDagRun(ctx, &id)
	if err != nil {
		return nil, fmt.Errorf("list dag run %s nodes: %w", id, err)
	}
	out := make([]domain.SessionRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, rowToRecord(gen.GetSessionRow(row)))
	}
	return out, nil
}

// ListSessionsByDagRunIDs is the batch form used by the run list.
func (s *Store) ListSessionsByDagRunIDs(ctx context.Context, ids []domain.DagRunID) (map[domain.DagRunID][]domain.SessionRecord, error) {
	out := make(map[domain.DagRunID][]domain.SessionRecord, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	raw := make([]*domain.DagRunID, 0, len(ids))
	for i := range ids {
		raw = append(raw, &ids[i])
	}
	rows, err := s.qr.ListSessionsByDagRunIDs(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("list dag run nodes: %w", err)
	}
	for _, row := range rows {
		if row.DagRunID == nil {
			continue
		}
		out[*row.DagRunID] = append(out[*row.DagRunID], rowToRecord(gen.GetSessionRow(row)))
	}
	return out, nil
}

// CancelDagRunNodes cancels every queued or blocked node of a run in one
// compare-and-set. Admitted or running nodes are left alone so a cancellation
// never kills work by accident.
func (s *Store) CancelDagRunNodes(ctx context.Context, id domain.DagRunID, at time.Time) (int64, error) {
	changed, err := s.qw.CancelDagRunNodes(ctx, gen.CancelDagRunNodesParams{
		UpdatedAt: at.UTC(),
		DagRunID:  &id,
	})
	if err != nil {
		return 0, fmt.Errorf("cancel dag run %s nodes: %w", id, err)
	}
	return changed, nil
}
