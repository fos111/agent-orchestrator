package controllers

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

// DagRunService is the DAG run surface the controller serves.
type DagRunService interface {
	Submit(ctx context.Context, projectID domain.ProjectID, spec domain.DagRunSpec) (domain.DagRunView, error)
	Get(ctx context.Context, id domain.DagRunID) (domain.DagRunView, error)
	List(ctx context.Context, projectID domain.ProjectID) ([]domain.DagRunView, error)
	Cancel(ctx context.Context, id domain.DagRunID, includeActive bool) (domain.DagRunView, error)
}

// DagRunsController owns the DAG run routes. A run is a coordinator, not a
// session: it groups parentless worker sessions and has no terminal of its own.
type DagRunsController struct {
	Svc DagRunService
}

// Register mounts the DAG run routes on r.
func (c *DagRunsController) Register(r chi.Router) {
	r.Post("/projects/{projectId}/dag-runs", c.submit)
	r.Get("/projects/{projectId}/dag-runs", c.list)
	r.Get("/dag-runs/{dagRunId}", c.get)
	r.Post("/dag-runs/{dagRunId}/cancel", c.cancel)
}

func (c *DagRunsController) submit(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/projects/{projectId}/dag-runs")
		return
	}
	var in CreateDagRunRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	nodes := make([]domain.DagNodeSpec, len(in.Nodes))
	for i, node := range in.Nodes {
		nodes[i] = domain.DagNodeSpec{
			ID:              node.ID,
			Name:            node.Name,
			Prompt:          node.Prompt,
			Harness:         node.Harness,
			DependsOn:       node.DependsOn,
			RequiredState:   node.RequiredState,
			RequestedOutput: node.RequestedOutput,
		}
	}
	view, err := c.Svc.Submit(r.Context(), dagRunProjectID(r), domain.DagRunSpec{Name: in.Name, Nodes: nodes})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, DagRunEnvelope{Run: dagRunResponse(view)})
}

func (c *DagRunsController) list(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/projects/{projectId}/dag-runs")
		return
	}
	views, err := c.Svc.List(r.Context(), dagRunProjectID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	out := ListDagRunsResponse{Runs: make([]DagRunResponse, 0, len(views))}
	for _, view := range views {
		out.Runs = append(out.Runs, dagRunResponse(view))
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func (c *DagRunsController) get(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/dag-runs/{dagRunId}")
		return
	}
	view, err := c.Svc.Get(r.Context(), dagRunID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, DagRunEnvelope{Run: dagRunResponse(view)})
}

func (c *DagRunsController) cancel(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/dag-runs/{dagRunId}/cancel")
		return
	}
	var in CancelDagRunRequest
	if err := decodeJSON(r, &in); err != nil && !errors.Is(err, io.EOF) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	view, err := c.Svc.Cancel(r.Context(), dagRunID(r), in.IncludeActive)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, DagRunEnvelope{Run: dagRunResponse(view)})
}

func dagRunID(r *http.Request) domain.DagRunID {
	return domain.DagRunID(chi.URLParam(r, "dagRunId"))
}

// dagRunProjectID reads the {projectId} route param. It is distinct from
// projectID, which serves the older /projects/{id} routes.
func dagRunProjectID(r *http.Request) domain.ProjectID {
	return domain.ProjectID(chi.URLParam(r, "projectId"))
}

func dagRunResponse(view domain.DagRunView) DagRunResponse {
	out := DagRunResponse{
		ID:                view.ID,
		ProjectID:         view.ProjectID,
		RootSessionID:     view.RootSessionID,
		Name:              view.Name,
		Status:            view.Status,
		CancelRequestedAt: view.CancelRequestedAt,
		CreatedAt:         view.CreatedAt,
		CompletedAt:       view.CompletedAt,
		Counts: DagRunCountsResponse{
			Total:     view.Counts.Total,
			Queued:    view.Counts.Queued,
			Blocked:   view.Counts.Blocked,
			Running:   view.Counts.Running,
			Completed: view.Counts.Completed,
			Failed:    view.Counts.Failed,
			Cancelled: view.Counts.Cancelled,
		},
		Nodes: make([]DagRunNodeResponse, 0, len(view.Nodes)),
	}
	for _, node := range view.Nodes {
		out.Nodes = append(out.Nodes, DagRunNodeResponse{
			ID:            node.NodeID,
			SessionID:     node.SessionID,
			Name:          node.Name,
			Status:        node.Status,
			BlockedReason: node.BlockedReason,
			DependsOn:     node.DependsOn,
			CreatedAt:     node.CreatedAt,
		})
	}
	return out
}
