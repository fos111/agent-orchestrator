package domain_test

import (
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func teamSession(id, parent domain.SessionID, kind domain.SessionKind, project domain.ProjectID) domain.SessionRecord {
	return domain.SessionRecord{
		ID:              id,
		ProjectID:       project,
		ParentSessionID: parent,
		Kind:            kind,
		DisplayName:     string(id),
		CreatedAt:       time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC),
	}
}

func teamIndexByID(t *testing.T, records ...domain.SessionRecord) func(domain.SessionID) (domain.SessionRecord, bool) {
	t.Helper()
	byID := make(map[domain.SessionID]domain.SessionRecord, len(records))
	for _, rec := range records {
		byID[rec.ID] = rec
	}
	return func(id domain.SessionID) (domain.SessionRecord, bool) {
		rec, ok := byID[id]
		return rec, ok
	}
}

func TestTeamRoot(t *testing.T) {
	project := domain.ProjectID("proj")
	tests := []struct {
		name   string
		target domain.SessionRecord
		all    []domain.SessionRecord
		want   domain.SessionID
		ok     bool
	}{
		{
			name:   "orchestrator is its own root",
			target: teamSession("ao-1", "", domain.KindOrchestrator, project),
			all:    []domain.SessionRecord{teamSession("ao-1", "", domain.KindOrchestrator, project)},
			want:   "ao-1",
			ok:     true,
		},
		{
			name:   "direct worker parent is the root",
			target: teamSession("ao-2", "ao-1", domain.KindWorker, project),
			all: []domain.SessionRecord{
				teamSession("ao-1", "", domain.KindOrchestrator, project),
				teamSession("ao-2", "ao-1", domain.KindWorker, project),
			},
			want: "ao-1",
			ok:   true,
		},
		{
			name:   "nested worker walks to the nearest orchestrator",
			target: teamSession("ao-3", "ao-2", domain.KindWorker, project),
			all: []domain.SessionRecord{
				teamSession("ao-1", "", domain.KindOrchestrator, project),
				teamSession("ao-2", "ao-1", domain.KindWorker, project),
				teamSession("ao-3", "ao-2", domain.KindWorker, project),
			},
			want: "ao-1",
			ok:   true,
		},
		{
			name:   "a nested orchestrator is its own root",
			target: teamSession("ao-2", "ao-1", domain.KindOrchestrator, project),
			all: []domain.SessionRecord{
				teamSession("ao-1", "", domain.KindOrchestrator, project),
				teamSession("ao-2", "ao-1", domain.KindOrchestrator, project),
			},
			want: "ao-2",
			ok:   true,
		},
		{
			name:   "worker with no orchestrator ancestor has no team",
			target: teamSession("ao-2", "ao-1", domain.KindWorker, project),
			all: []domain.SessionRecord{
				teamSession("ao-1", "", domain.KindWorker, project),
				teamSession("ao-2", "ao-1", domain.KindWorker, project),
			},
			want: "",
			ok:   false,
		},
		{
			name:   "desktop standalone worker has no team",
			target: teamSession("ao-9", "", domain.KindWorker, project),
			all:    []domain.SessionRecord{teamSession("ao-9", "", domain.KindWorker, project)},
			want:   "",
			ok:     false,
		},
		{
			name:   "missing parent stops the walk",
			target: teamSession("ao-2", "ao-1", domain.KindWorker, project),
			all:    []domain.SessionRecord{teamSession("ao-2", "ao-1", domain.KindWorker, project)},
			want:   "",
			ok:     false,
		},
		{
			name:   "cross-project parent stops the walk",
			target: teamSession("ao-2", "ao-1", domain.KindWorker, project),
			all: []domain.SessionRecord{
				teamSession("ao-1", "", domain.KindOrchestrator, "other"),
				teamSession("ao-2", "ao-1", domain.KindWorker, project),
			},
			want: "",
			ok:   false,
		},
		{
			name:   "cycle stops the walk",
			target: teamSession("ao-2", "ao-1", domain.KindWorker, project),
			all: []domain.SessionRecord{
				teamSession("ao-1", "ao-2", domain.KindWorker, project),
				teamSession("ao-2", "ao-1", domain.KindWorker, project),
			},
			want: "",
			ok:   false,
		},
		{
			name:   "terminated orchestrator still roots its team",
			target: teamSession("ao-2", "ao-1", domain.KindWorker, project),
			all: []domain.SessionRecord{
				func() domain.SessionRecord {
					rec := teamSession("ao-1", "", domain.KindOrchestrator, project)
					rec.IsTerminated = true
					return rec
				}(),
				teamSession("ao-2", "ao-1", domain.KindWorker, project),
			},
			want: "ao-1",
			ok:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := domain.TeamRoot(tt.target, teamIndexByID(t, tt.all...))
			if ok != tt.ok || got != tt.want {
				t.Fatalf("TeamRoot() = (%q, %v), want (%q, %v)", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestTeamIndexMembership(t *testing.T) {
	project := domain.ProjectID("proj")
	base := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	at := func(minutes int) time.Time { return base.Add(time.Duration(minutes) * time.Minute) }

	lead := teamSession("ao-1", "", domain.KindOrchestrator, project)
	lead.CreatedAt = at(0)
	worker := teamSession("ao-2", "ao-1", domain.KindWorker, project)
	worker.CreatedAt = at(1)
	terminated := teamSession("ao-3", "ao-1", domain.KindWorker, project)
	terminated.CreatedAt = at(2)
	terminated.IsTerminated = true
	hidden := teamSession("ao-4", "ao-1", domain.KindWorker, project)
	hidden.CreatedAt = at(3)
	hidden.IsTaskPreparation = true
	standalone := teamSession("ao-5", "", domain.KindWorker, project)
	standalone.CreatedAt = at(4)
	otherLead := teamSession("ao-6", "", domain.KindOrchestrator, project)
	otherLead.CreatedAt = at(5)
	blocked := teamSession("ao-7", "ao-1", domain.KindWorker, project)
	blocked.CreatedAt = at(6)
	blocked.SchedulingState = domain.SchedulingBlocked
	blocked.BlockedReason = string(domain.BlockedTeamLimit)

	idx := domain.NewTeamIndex([]domain.SessionRecord{standalone, terminated, worker, lead, hidden, otherLead, blocked})

	if roots := idx.Roots(); len(roots) != 2 || roots[0] != "ao-1" || roots[1] != "ao-6" {
		t.Fatalf("Roots() = %v, want [ao-1 ao-6]", roots)
	}
	members := idx.Members("ao-1")
	if len(members) != 3 || members[0].SessionID != "ao-1" || members[1].SessionID != "ao-2" || members[2].SessionID != "ao-7" {
		t.Fatalf("Members(ao-1) = %+v, want lead, active worker, and blocked candidate", members)
	}
	if _, ok := idx.Record("ao-4"); ok {
		t.Fatal("hidden task-preparation row must not be indexed")
	}
	if root, ok := idx.TeamRoot("ao-5"); ok || root != "" {
		t.Fatalf("TeamRoot(standalone) = (%q, %v), want no team", root, ok)
	}

	team, ok := idx.Team("ao-1", domain.DefaultMaxActivePerTeam)
	if !ok {
		t.Fatal("Team(ao-1) not found")
	}
	if team.RootSessionID != "ao-1" || team.ProjectID != project {
		t.Fatalf("team identity = %+v", team)
	}
	if team.ActiveWorkers != 1 {
		t.Fatalf("ActiveWorkers = %d, want 1 (lead exempt, terminated and blocked excluded)", team.ActiveWorkers)
	}
	if team.Orchestrator.SessionID != "ao-1" {
		t.Fatalf("Orchestrator = %+v", team.Orchestrator)
	}
	if team.LimitReached() {
		t.Fatal("one active worker must not reach the default limit of four")
	}

	teamFor, ok := idx.TeamFor("ao-2", 1)
	if !ok || teamFor.RootSessionID != "ao-1" {
		t.Fatalf("TeamFor(ao-2) = (%+v, %v)", teamFor, ok)
	}
	if !teamFor.LimitReached() {
		t.Fatal("one active worker must reach a limit of one")
	}

	teams := idx.Teams(0)
	if len(teams) != 2 || teams[0].RootSessionID != "ao-1" || teams[1].RootSessionID != "ao-6" {
		t.Fatalf("Teams() = %+v, want two teams in creation order", teams)
	}
	if teams[0].LimitReached() {
		t.Fatal("a zero limit means unlimited")
	}
}

func TestTeamMessageValidate(t *testing.T) {
	valid := domain.TeamMessage{
		ID:                "tm-1",
		ProjectID:         "proj",
		TeamRootSessionID: "ao-1",
		SenderSessionID:   "ao-1",
		Body:              "Rebase on main before continuing.",
		CreatedAt:         time.Now().UTC(),
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
	long := valid
	long.Body = strings.Repeat("x", domain.MaxTeamMessageBodyLength+1)
	if err := long.Validate(); err == nil {
		t.Fatal("Validate() accepted an over-long body")
	}
	empty := valid
	empty.Body = ""
	if err := empty.Validate(); err == nil {
		t.Fatal("Validate() accepted an empty body")
	}
	missingRoot := valid
	missingRoot.TeamRootSessionID = ""
	if err := missingRoot.Validate(); err == nil {
		t.Fatal("Validate() accepted a missing root")
	}
}

func TestTeamMessageReportCounts(t *testing.T) {
	report := domain.TeamMessageReport{Deliveries: []domain.TeamMessageDelivery{
		{Status: domain.TeamDeliveryDelivered},
		{Status: domain.TeamDeliveryDelivered},
		{Status: domain.TeamDeliveryFailed},
		{Status: domain.TeamDeliveryPending},
	}}
	if got := report.DeliveredCount(); got != 2 {
		t.Fatalf("DeliveredCount() = %d, want 2", got)
	}
	if got := report.FailedCount(); got != 1 {
		t.Fatalf("FailedCount() = %d, want 1", got)
	}
}
