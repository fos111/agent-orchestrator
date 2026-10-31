package controllers_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
)

type fakeTeamService struct {
	team       domain.Team
	teams      []domain.Team
	report     domain.TeamMessageReport
	err        error
	gotProject domain.ProjectID
	gotRoot    domain.SessionID
	gotSender  domain.SessionID
	gotBody    string
	broadcasts int
}

func (f *fakeTeamService) Status(_ context.Context, rootID domain.SessionID) (domain.Team, error) {
	f.gotRoot = rootID
	return f.team, f.err
}

func (f *fakeTeamService) List(_ context.Context, projectID domain.ProjectID) ([]domain.Team, error) {
	f.gotProject = projectID
	return f.teams, f.err
}

func (f *fakeTeamService) Broadcast(_ context.Context, rootID, senderSessionID domain.SessionID, body string) (domain.TeamMessageReport, error) {
	f.broadcasts++
	f.gotRoot, f.gotSender, f.gotBody = rootID, senderSessionID, body
	return f.report, f.err
}

func newTeamTestServer(t *testing.T, svc controllers.TeamService) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Teams: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func sampleTeam() domain.Team {
	created := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	lead := domain.TeamMember{SessionID: "ao-100", Kind: domain.KindOrchestrator, CreatedAt: created}
	worker := domain.TeamMember{SessionID: "ao-101", ParentSessionID: "ao-100", Kind: domain.KindWorker, CreatedAt: created}
	return domain.Team{
		RootSessionID: "ao-100", ProjectID: "ao", Orchestrator: lead,
		Members: []domain.TeamMember{lead, worker}, ActiveWorkers: 1, MaxActiveWorkers: 4,
	}
}

func TestTeamsAPI_ListsProjectTeams(t *testing.T) {
	svc := &fakeTeamService{teams: []domain.Team{sampleTeam()}}
	srv := newTeamTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/projects/ao/teams", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	if svc.gotProject != "ao" {
		t.Errorf("project = %q, want ao", svc.gotProject)
	}
	var resp struct {
		Teams []struct {
			RootSessionID string `json:"rootSessionId"`
			ActiveWorkers int    `json:"activeWorkers"`
			Members       []struct {
				SessionID string `json:"sessionId"`
			} `json:"members"`
		} `json:"teams"`
	}
	mustJSON(t, body, &resp)
	if len(resp.Teams) != 1 || resp.Teams[0].RootSessionID != "ao-100" || resp.Teams[0].ActiveWorkers != 1 || len(resp.Teams[0].Members) != 2 {
		t.Fatalf("teams = %+v", resp.Teams)
	}
}

func TestTeamsAPI_EmptyListIsAnArray(t *testing.T) {
	srv := newTeamTestServer(t, &fakeTeamService{})
	body, status, _ := doRequest(t, srv, "GET", "/api/v1/projects/ao/teams", "")
	if status != http.StatusOK || string(body) == "" {
		t.Fatalf("status = %d body=%s", status, body)
	}
	var resp struct {
		Teams []domain.Team `json:"teams"`
	}
	mustJSON(t, body, &resp)
	if resp.Teams == nil || len(resp.Teams) != 0 {
		t.Fatalf("teams = %#v, want an empty array", resp.Teams)
	}
}

func TestTeamsAPI_StatusReturnsDerivedTeam(t *testing.T) {
	svc := &fakeTeamService{team: sampleTeam()}
	srv := newTeamTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/teams/ao-100", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	if svc.gotRoot != "ao-100" {
		t.Errorf("root = %q, want ao-100", svc.gotRoot)
	}
	var resp struct {
		Team struct {
			RootSessionID    string `json:"rootSessionId"`
			MaxActiveWorkers int    `json:"maxActiveWorkers"`
			Orchestrator     struct {
				SessionID string `json:"sessionId"`
			} `json:"orchestrator"`
		} `json:"team"`
	}
	mustJSON(t, body, &resp)
	if resp.Team.RootSessionID != "ao-100" || resp.Team.MaxActiveWorkers != 4 || resp.Team.Orchestrator.SessionID != "ao-100" {
		t.Fatalf("team = %+v", resp.Team)
	}
}

func TestTeamsAPI_StatusMapsMissingTeamTo404(t *testing.T) {
	svc := &fakeTeamService{err: apierr.NotFound("TEAM_NOT_FOUND", "Unknown team root or the session is not an orchestrator")}
	srv := newTeamTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/teams/ao-999", "")
	if status != http.StatusNotFound || !strings.Contains(string(body), "TEAM_NOT_FOUND") {
		t.Fatalf("status = %d body=%s", status, body)
	}
}

func TestTeamsAPI_BroadcastPassesAttributionAndReportsCounts(t *testing.T) {
	created := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	svc := &fakeTeamService{report: domain.TeamMessageReport{
		Message: domain.TeamMessage{
			ID: "msg-1", ProjectID: "ao", TeamRootSessionID: "ao-100",
			SenderSessionID: "ao-100", Body: "Rebase on main.", CreatedAt: created,
		},
		Deliveries: []domain.TeamMessageDelivery{
			{MessageID: "msg-1", SessionID: "ao-101", Status: domain.TeamDeliveryDelivered},
			{MessageID: "msg-1", SessionID: "ao-102", Status: domain.TeamDeliveryFailed, Error: "runtime unavailable"},
		},
	}}
	srv := newTeamTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/teams/ao-100/messages",
		`{"senderSessionId":" ao-100 ","message":"Rebase\non main."}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	if svc.broadcasts != 1 || svc.gotRoot != "ao-100" || svc.gotSender != "ao-100" || svc.gotBody != "Rebase\non main." {
		t.Fatalf("broadcast got root=%q sender=%q body=%q", svc.gotRoot, svc.gotSender, svc.gotBody)
	}
	var resp struct {
		OK        bool `json:"ok"`
		Delivered int  `json:"delivered"`
		Failed    int  `json:"failed"`
		Message   struct {
			ID   string `json:"id"`
			Body string `json:"body"`
		} `json:"message"`
		Deliveries []struct {
			SessionID string `json:"sessionId"`
			Status    string `json:"status"`
			Error     string `json:"error"`
		} `json:"deliveries"`
	}
	mustJSON(t, body, &resp)
	if !resp.OK || resp.Delivered != 1 || resp.Failed != 1 || resp.Message.ID != "msg-1" || len(resp.Deliveries) != 2 {
		t.Fatalf("response = %+v", resp)
	}
	if resp.Deliveries[1].Status != "failed" || resp.Deliveries[1].Error != "runtime unavailable" {
		t.Fatalf("deliveries = %+v", resp.Deliveries)
	}
}

func TestTeamsAPI_BroadcastRejectsEmptyMessage(t *testing.T) {
	svc := &fakeTeamService{}
	srv := newTeamTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/teams/ao-100/messages", `{"message":"   "}`)
	if status != http.StatusBadRequest || !strings.Contains(string(body), "TEAM_MESSAGE_REQUIRED") {
		t.Fatalf("status = %d body=%s", status, body)
	}
	if svc.broadcasts != 0 {
		t.Fatal("empty message reached the service")
	}
}

func TestTeamsAPI_BroadcastRejectsInvalidJSON(t *testing.T) {
	svc := &fakeTeamService{}
	srv := newTeamTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/teams/ao-100/messages", `{"message":`)
	if status != http.StatusBadRequest || !strings.Contains(string(body), "INVALID_JSON") {
		t.Fatalf("status = %d body=%s", status, body)
	}
	if svc.broadcasts != 0 {
		t.Fatal("invalid JSON reached the service")
	}
}

func TestTeamsAPI_NilServiceAnswers501(t *testing.T) {
	srv := newTeamTestServer(t, nil)
	for _, route := range []struct{ method, path string }{
		{"GET", "/api/v1/projects/ao/teams"},
		{"GET", "/api/v1/teams/ao-100"},
		{"POST", "/api/v1/teams/ao-100/messages"},
	} {
		body, status, _ := doRequest(t, srv, route.method, route.path, "{}")
		if status != http.StatusNotImplemented {
			t.Fatalf("%s %s status = %d body=%s, want 501", route.method, route.path, status, body)
		}
	}
}
