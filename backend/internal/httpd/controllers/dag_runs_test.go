package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

type fakeDagRunService struct {
	submittedProject domain.ProjectID
	submitted        domain.DagRunSpec
	cancelled        domain.DagRunID
	includeActive    bool
	runs             []domain.DagRunView
}

func (f *fakeDagRunService) Submit(_ context.Context, projectID domain.ProjectID, spec domain.DagRunSpec) (domain.DagRunView, error) {
	f.submittedProject = projectID
	f.submitted = spec
	return dagRunView(), nil
}

func (f *fakeDagRunService) Get(_ context.Context, id domain.DagRunID) (domain.DagRunView, error) {
	if id == "missing" {
		return domain.DagRunView{}, apierr.NotFound("DAG_RUN_NOT_FOUND", "Unknown DAG run")
	}
	return dagRunView(), nil
}

func (f *fakeDagRunService) List(context.Context, domain.ProjectID) ([]domain.DagRunView, error) {
	return f.runs, nil
}

func (f *fakeDagRunService) Cancel(_ context.Context, id domain.DagRunID, includeActive bool) (domain.DagRunView, error) {
	f.cancelled = id
	f.includeActive = includeActive
	return dagRunView(), nil
}

func dagRunView() domain.DagRunView {
	now := time.Date(2026, 10, 31, 4, 0, 0, 0, time.UTC)
	return domain.DagRunView{
		ID:        "dag-1",
		ProjectID: "p",
		Name:      "feature-x",
		Status:    domain.DagRunPending,
		CreatedAt: now,
		Counts:    domain.DagRunCounts{Total: 2, Queued: 1, Blocked: 1},
		Nodes: []domain.DagNodeView{
			{NodeID: "build", SessionID: "p-1", Name: "Build", Status: domain.DagNodeQueued, CreatedAt: now},
			{NodeID: "test", SessionID: "p-2", Name: "Test", Status: domain.DagNodeBlocked, BlockedReason: "dependency_pending", DependsOn: []domain.DagNodeID{"build"}, CreatedAt: now},
		},
	}
}

func dagRunTestRouter(service DagRunService) http.Handler {
	r := chi.NewRouter()
	(&DagRunsController{Svc: service}).Register(r)
	return r
}

func TestDagRunsSubmitMapsEveryNodeField(t *testing.T) {
	service := &fakeDagRunService{}
	body := `{"name":"feature-x","nodes":[
		{"id":"build","name":"Build","prompt":"Implement","harness":"claude-code"},
		{"id":"test","name":"Test","prompt":"Verify","dependsOn":["build"],"requiredState":"completed","requestedOutput":"artifact"}
	]}`
	req := httptest.NewRequest(http.MethodPost, "/projects/p/dag-runs", strings.NewReader(body))
	rec := httptest.NewRecorder()
	dagRunTestRouter(service).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.submittedProject != "p" || service.submitted.Name != "feature-x" || len(service.submitted.Nodes) != 2 {
		t.Fatalf("submitted = %#v", service.submitted)
	}
	node := service.submitted.Nodes[1]
	if node.ID != "test" || len(node.DependsOn) != 1 || node.DependsOn[0] != "build" ||
		node.RequiredState != domain.DagRequiredCompleted || node.RequestedOutput != domain.RequestedOutputArtifact {
		t.Fatalf("node mapping = %#v", node)
	}

	var response DagRunEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Run.ID != "dag-1" || response.Run.Status != domain.DagRunPending {
		t.Fatalf("response = %#v", response.Run)
	}
	if response.Run.Counts != (DagRunCountsResponse{Total: 2, Queued: 1, Blocked: 1}) {
		t.Fatalf("counts = %#v", response.Run.Counts)
	}
	if len(response.Run.Nodes) != 2 || response.Run.Nodes[1].SessionID != "p-2" || response.Run.Nodes[1].BlockedReason != "dependency_pending" {
		t.Fatalf("nodes = %#v", response.Run.Nodes)
	}
}

func TestDagRunsListWrapsRuns(t *testing.T) {
	service := &fakeDagRunService{runs: []domain.DagRunView{dagRunView()}}
	req := httptest.NewRequest(http.MethodGet, "/projects/p/dag-runs", nil)
	rec := httptest.NewRecorder()
	dagRunTestRouter(service).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response ListDagRunsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Runs) != 1 || response.Runs[0].ID != "dag-1" {
		t.Fatalf("runs = %#v", response.Runs)
	}
}

func TestDagRunsGetRendersNotFoundEnvelope(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/dag-runs/missing", nil)
	rec := httptest.NewRecorder()
	dagRunTestRouter(&fakeDagRunService{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "DAG_RUN_NOT_FOUND") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDagRunsCancelAcceptsEmptyBody(t *testing.T) {
	service := &fakeDagRunService{}
	req := httptest.NewRequest(http.MethodPost, "/dag-runs/dag-1/cancel", nil)
	rec := httptest.NewRecorder()
	dagRunTestRouter(service).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if service.cancelled != "dag-1" || service.includeActive {
		t.Fatalf("cancel = %q includeActive=%v", service.cancelled, service.includeActive)
	}
}

func TestDagRunsCancelForwardsIncludeActive(t *testing.T) {
	service := &fakeDagRunService{}
	req := httptest.NewRequest(http.MethodPost, "/dag-runs/dag-1/cancel", strings.NewReader(`{"includeActive":true}`))
	rec := httptest.NewRecorder()
	dagRunTestRouter(service).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !service.includeActive {
		t.Fatalf("status=%d includeActive=%v", rec.Code, service.includeActive)
	}
}

func TestDagRunsCancelRejectsMalformedBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/dag-runs/dag-1/cancel", strings.NewReader(`{"includeActive":`))
	rec := httptest.NewRecorder()
	dagRunTestRouter(&fakeDagRunService{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "INVALID_JSON") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
