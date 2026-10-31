package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func sampleDagRun(id, project, name string) domain.DagRun {
	return domain.DagRun{
		ID:        domain.DagRunID(id),
		ProjectID: domain.ProjectID(project),
		Name:      name,
		Status:    domain.DagRunPending,
		CreatedAt: time.Now().UTC().Truncate(time.Second),
	}
}

func TestDagRunInsertSelectAndList(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "dag")

	older := sampleDagRun("run-older", "dag", "older")
	if err := s.InsertDagRun(ctx, older); err != nil {
		t.Fatalf("insert older run: %v", err)
	}
	newer := sampleDagRun("run-newer", "dag", "newer")
	newer.CreatedAt = older.CreatedAt.Add(time.Minute)
	if err := s.InsertDagRun(ctx, newer); err != nil {
		t.Fatalf("insert newer run: %v", err)
	}

	got, ok, err := s.SelectDagRunByID(ctx, older.ID)
	if err != nil || !ok {
		t.Fatalf("select run: ok=%v err=%v", ok, err)
	}
	if got.Name != older.Name || got.Status != domain.DagRunPending || got.ProjectID != older.ProjectID {
		t.Fatalf("round-trip mismatch: got %+v want %+v", got, older)
	}
	if got.RootSessionID != nil || got.CancelRequestedAt != nil || got.CompletedAt != nil {
		t.Fatalf("new run has unexpected optional fields: %+v", got)
	}
	if !got.CreatedAt.Equal(older.CreatedAt) {
		t.Fatalf("createdAt = %v, want %v", got.CreatedAt, older.CreatedAt)
	}

	if _, ok, err := s.SelectDagRunByID(ctx, "run-missing"); err != nil || ok {
		t.Fatalf("select missing run: ok=%v err=%v", ok, err)
	}

	runs, err := s.SelectDagRunsByProject(ctx, "dag")
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 2 || runs[0].ID != "run-newer" || runs[1].ID != "run-older" {
		t.Fatalf("runs = %+v, want newest first", runs)
	}
}

func TestDagRunStatusAndCancellationAreMonotonic(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "dag")
	run := sampleDagRun("run-1", "dag", "feature-x")
	if err := s.InsertDagRun(ctx, run); err != nil {
		t.Fatalf("insert run: %v", err)
	}

	changed, err := s.UpdateDagRunStatus(ctx, run.ID, domain.DagRunRunning, nil)
	if err != nil || !changed {
		t.Fatalf("pending -> running: changed=%v err=%v", changed, err)
	}

	cancelledAt := run.CreatedAt.Add(time.Minute)
	changed, err = s.CancelDagRun(ctx, run.ID, cancelledAt)
	if err != nil || !changed {
		t.Fatalf("cancel running run: changed=%v err=%v", changed, err)
	}
	got, ok, err := s.SelectDagRunByID(ctx, run.ID)
	if err != nil || !ok {
		t.Fatalf("select cancelled run: ok=%v err=%v", ok, err)
	}
	if got.Status != domain.DagRunCancelled || got.CancelRequestedAt == nil || got.CompletedAt == nil {
		t.Fatalf("cancelled run = %+v", got)
	}
	if !got.CancelRequestedAt.Equal(cancelledAt) || !got.CompletedAt.Equal(cancelledAt) {
		t.Fatalf("cancel timestamps = %v / %v, want %v", got.CancelRequestedAt, got.CompletedAt, cancelledAt)
	}

	// A cancelled run never moves again, and cancelling twice is a no-op the
	// caller can treat as already cancelled.
	if changed, err := s.UpdateDagRunStatus(ctx, run.ID, domain.DagRunCompleted, &cancelledAt); err != nil || changed {
		t.Fatalf("update cancelled run: changed=%v err=%v", changed, err)
	}
	if changed, err := s.CancelDagRun(ctx, run.ID, cancelledAt); err != nil || changed {
		t.Fatalf("cancel cancelled run: changed=%v err=%v", changed, err)
	}
}

func TestDagRunRejectsUnknownProject(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	run := sampleDagRun("run-1", "missing", "orphan")
	if err := s.InsertDagRun(ctx, run); err == nil {
		t.Fatal("insert run for an unregistered project succeeded")
	}
}
