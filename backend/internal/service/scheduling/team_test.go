package scheduling_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/scheduling"
)

type fakeSessionReader struct {
	records map[domain.SessionID]domain.SessionRecord
	listErr error
	getErr  error
}

func (f *fakeSessionReader) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	if f.getErr != nil {
		return domain.SessionRecord{}, false, f.getErr
	}
	rec, ok := f.records[id]
	return rec, ok, nil
}

func (f *fakeSessionReader) ListSessions(_ context.Context, project domain.ProjectID) ([]domain.SessionRecord, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []domain.SessionRecord
	for _, rec := range f.records {
		if rec.ProjectID == project {
			out = append(out, rec)
		}
	}
	return out, nil
}

func (f *fakeSessionReader) ListAllSessions(context.Context) ([]domain.SessionRecord, error) {
	return nil, nil
}

func schedulingRecord(id, parent domain.SessionID, kind domain.SessionKind, project domain.ProjectID) domain.SessionRecord {
	return domain.SessionRecord{
		ID:              id,
		ProjectID:       project,
		ParentSessionID: parent,
		Kind:            kind,
		CreatedAt:       time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC),
	}
}

func TestTeamServiceTeamDerivation(t *testing.T) {
	project := domain.ProjectID("proj")
	reader := &fakeSessionReader{records: map[domain.SessionID]domain.SessionRecord{
		"ao-1": schedulingRecord("ao-1", "", domain.KindOrchestrator, project),
		"ao-2": schedulingRecord("ao-2", "ao-1", domain.KindWorker, project),
		"ao-3": schedulingRecord("ao-3", "ao-2", domain.KindWorker, project),
		"ao-4": schedulingRecord("ao-4", "", domain.KindWorker, project),
	}}
	svc := scheduling.NewTeamService(reader)

	root, ok, err := svc.RootFor(context.Background(), "ao-3")
	if err != nil || !ok || root != "ao-1" {
		t.Fatalf("RootFor(ao-3) = (%q, %v, %v)", root, ok, err)
	}

	team, ok, err := svc.TeamFor(context.Background(), "ao-3")
	if err != nil || !ok {
		t.Fatalf("TeamFor(ao-3) = (%v, %v)", ok, err)
	}
	if team.RootSessionID != "ao-1" || team.ActiveWorkers != 2 || len(team.Members) != 3 {
		t.Fatalf("team = %+v", team)
	}
	if team.MaxActiveWorkers != domain.DefaultMaxActivePerTeam {
		t.Fatalf("MaxActiveWorkers = %d, want default %d", team.MaxActiveWorkers, domain.DefaultMaxActivePerTeam)
	}

	if _, ok, err := svc.TeamFor(context.Background(), "ao-4"); err != nil || ok {
		t.Fatalf("standalone worker TeamFor ok=%v err=%v, want no team", ok, err)
	}
	if _, ok, err := svc.RootFor(context.Background(), "missing"); err != nil || ok {
		t.Fatalf("missing RootFor ok=%v err=%v, want no team", ok, err)
	}

	if _, err := svc.Team(context.Background(), "ao-2"); !errors.Is(err, scheduling.ErrTeamNotFound) {
		t.Fatalf("Team(worker) err = %v, want ErrTeamNotFound", err)
	}
	if _, err := svc.Team(context.Background(), "missing"); !errors.Is(err, scheduling.ErrTeamNotFound) {
		t.Fatalf("Team(missing) err = %v, want ErrTeamNotFound", err)
	}

	teams, err := svc.Teams(context.Background(), project)
	if err != nil {
		t.Fatalf("Teams() error = %v", err)
	}
	if len(teams) != 1 || teams[0].RootSessionID != "ao-1" {
		t.Fatalf("Teams() = %+v", teams)
	}

	svc.MaxActiveWorkers = 1
	team, ok, err = svc.TeamFor(context.Background(), "ao-2")
	if err != nil || !ok {
		t.Fatalf("TeamFor(ao-2) = (%v, %v)", ok, err)
	}
	if !team.LimitReached() {
		t.Fatalf("LimitReached() = false for %+v", team)
	}
}

func TestTeamServicePropagatesReadErrors(t *testing.T) {
	want := errors.New("boom")
	svc := scheduling.NewTeamService(&fakeSessionReader{records: map[domain.SessionID]domain.SessionRecord{
		"ao-1": schedulingRecord("ao-1", "", domain.KindOrchestrator, "proj"),
	}, listErr: want})
	if _, err := svc.Teams(context.Background(), "proj"); !errors.Is(err, want) {
		t.Fatalf("Teams() error = %v, want %v", err, want)
	}

	svc = scheduling.NewTeamService(&fakeSessionReader{records: map[domain.SessionID]domain.SessionRecord{}, getErr: want})
	if _, _, err := svc.TeamFor(context.Background(), "ao-1"); !errors.Is(err, want) {
		t.Fatalf("TeamFor() error = %v, want %v", err, want)
	}
}
