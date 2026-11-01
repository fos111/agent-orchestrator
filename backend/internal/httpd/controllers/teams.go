package controllers

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

// maxTeamBroadcastBodyBytes bounds the raw request body before decoding. A
// broadcast body is capped at MaxTeamMessageBodyLength characters, which is at
// most four UTF-8 bytes each; the rest is JSON envelope headroom.
const maxTeamBroadcastBodyBytes = domain.MaxTeamMessageBodyLength*4 + (64 << 10)

// TeamService is the controller-facing team surface: derived status and the
// best-effort broadcast.
type TeamService interface {
	Status(ctx context.Context, rootID domain.SessionID) (domain.Team, error)
	List(ctx context.Context, projectID domain.ProjectID) ([]domain.Team, error)
	Broadcast(ctx context.Context, rootID, senderSessionID domain.SessionID, body string) (domain.TeamMessageReport, error)
}

// TeamsController owns the derived team routes. A team has no create or delete
// route: membership is the spawn-parent tree rooted at an orchestrator.
type TeamsController struct {
	Svc TeamService
}

// Register mounts the bounded team REST routes.
func (c *TeamsController) Register(r chi.Router) {
	r.Get("/projects/{projectId}/teams", c.list)
	r.Get("/teams/{rootSessionId}", c.status)
	r.Post("/teams/{rootSessionId}/messages", c.broadcast)
}

func (c *TeamsController) list(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/projects/{projectId}/teams")
		return
	}
	teams, err := c.Svc.List(r.Context(), domain.ProjectID(chi.URLParam(r, "projectId")))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	if teams == nil {
		teams = []domain.Team{}
	}
	envelope.WriteJSON(w, http.StatusOK, ListTeamsResponse{Teams: teams})
}

func (c *TeamsController) status(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/teams/{rootSessionId}")
		return
	}
	team, err := c.Svc.Status(r.Context(), domain.SessionID(chi.URLParam(r, "rootSessionId")))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, TeamEnvelope{Team: team})
}

func (c *TeamsController) broadcast(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/teams/{rootSessionId}/messages")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxTeamBroadcastBodyBytes)
	var in BroadcastTeamMessageRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	message := domain.SanitizeControlChars(in.Message)
	if strings.TrimSpace(message) == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "TEAM_MESSAGE_REQUIRED", "Message is required", nil)
		return
	}
	report, err := c.Svc.Broadcast(
		r.Context(),
		domain.SessionID(chi.URLParam(r, "rootSessionId")),
		domain.SessionID(strings.TrimSpace(in.SenderSessionID)),
		message,
	)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, BroadcastTeamMessageResponse{
		OK:         true,
		Message:    report.Message,
		Deliveries: report.Deliveries,
		Delivered:  report.DeliveredCount(),
		Failed:     report.FailedCount(),
	})
}
