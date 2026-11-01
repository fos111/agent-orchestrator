// Package scheduling owns the derivation and admission rules that decide
// whether a session may launch. Team derivation lives here so both the read
// surfaces and the admission path consume one definition of a team.
package scheduling

import (
	"context"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ErrTeamNotFound is returned when a root exists but is not an orchestrator, or
// does not exist at all. A non-team session is not an error on TeamFor.
var ErrTeamNotFound = errors.New("team not found")

// SessionReader is the read-only session source team derivation needs. The
// SQLite store satisfies it directly.
type SessionReader interface {
	GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error)
	ListSessions(context.Context, domain.ProjectID) ([]domain.SessionRecord, error)
	ListAllSessions(context.Context) ([]domain.SessionRecord, error)
}

// ProjectReader reads one project's config so the effective team limit can
// follow MaxActivePerTeam. The SQLite store satisfies it directly.
type ProjectReader interface {
	GetProject(context.Context, string) (domain.ProjectRecord, bool, error)
}

// TeamService derives teams from the durable spawn-parent tree.
//
// MaxActiveWorkers is the fallback concurrent-active-worker limit, used when no
// ProjectReader is wired or a project cannot be read. The project's
// MaxActivePerTeam otherwise wins. The lead is never counted and no derivation
// here launches, pauses, or kills a session.
type TeamService struct {
	sessions         SessionReader
	projects         ProjectReader
	MaxActiveWorkers int
}

// NewTeamService builds a derivation service with the product default limit.
func NewTeamService(sessions SessionReader) *TeamService {
	return &TeamService{sessions: sessions, MaxActiveWorkers: domain.DefaultMaxActivePerTeam}
}

// NewTeamServiceWithProjects builds a derivation service that resolves each
// project's MaxActivePerTeam, falling back to the product default.
func NewTeamServiceWithProjects(sessions SessionReader, projects ProjectReader) *TeamService {
	svc := NewTeamService(sessions)
	svc.projects = projects
	return svc
}

// RootFor returns the team root containing sessionID. ok is false when the
// session has no orchestrator ancestor.
func (s *TeamService) RootFor(ctx context.Context, sessionID domain.SessionID) (domain.SessionID, bool, error) {
	rec, ok, err := s.sessions.GetSession(ctx, sessionID)
	if err != nil {
		return "", false, fmt.Errorf("resolve team root for %s: %w", sessionID, err)
	}
	if !ok {
		return "", false, nil
	}
	records, err := s.projectIndexRecords(ctx, rec.ProjectID)
	if err != nil {
		return "", false, err
	}
	root, ok := domain.NewTeamIndex(records).TeamRoot(rec.ID)
	return root, ok, nil
}

// TeamFor returns the team containing sessionID. ok is false for a standalone
// worker, a desktop New Task session, or a DAG node: anything with no
// orchestrator ancestor.
func (s *TeamService) TeamFor(ctx context.Context, sessionID domain.SessionID) (domain.Team, bool, error) {
	rec, ok, err := s.sessions.GetSession(ctx, sessionID)
	if err != nil {
		return domain.Team{}, false, fmt.Errorf("derive team for %s: %w", sessionID, err)
	}
	if !ok {
		return domain.Team{}, false, nil
	}
	records, err := s.projectIndexRecords(ctx, rec.ProjectID)
	if err != nil {
		return domain.Team{}, false, err
	}
	team, ok := domain.NewTeamIndex(records).TeamFor(rec.ID, s.effectiveLimit(ctx, rec.ProjectID))
	return team, ok, nil
}

// Team returns the team rooted at rootID. ErrTeamNotFound means the id does not
// exist or is not an orchestrator.
func (s *TeamService) Team(ctx context.Context, rootID domain.SessionID) (domain.Team, error) {
	rec, ok, err := s.sessions.GetSession(ctx, rootID)
	if err != nil {
		return domain.Team{}, fmt.Errorf("derive team %s: %w", rootID, err)
	}
	if !ok || rec.Kind != domain.KindOrchestrator {
		return domain.Team{}, ErrTeamNotFound
	}
	records, err := s.projectIndexRecords(ctx, rec.ProjectID)
	if err != nil {
		return domain.Team{}, err
	}
	team, ok := domain.NewTeamIndex(records).Team(rec.ID, s.effectiveLimit(ctx, rec.ProjectID))
	if !ok {
		return domain.Team{}, ErrTeamNotFound
	}
	return team, nil
}

// Teams returns every team in a project, in lead creation order.
func (s *TeamService) Teams(ctx context.Context, projectID domain.ProjectID) ([]domain.Team, error) {
	records, err := s.projectIndexRecords(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, nil
	}
	return domain.NewTeamIndex(records).Teams(s.effectiveLimit(ctx, projectID)), nil
}

// effectiveLimit resolves the project's team cap. A zero or unreadable config
// falls back to the service default.
func (s *TeamService) effectiveLimit(ctx context.Context, projectID domain.ProjectID) int {
	if s.projects == nil {
		return s.MaxActiveWorkers
	}
	project, ok, err := s.projects.GetProject(ctx, string(projectID))
	if err != nil || !ok {
		return s.MaxActiveWorkers
	}
	return project.Config.Scheduling.TeamLimit()
}

// TeamAdmission tracks derived-team worker occupancy during one reconcile.
// Counting uses the same predicate as the project slot count, so queued and
// blocked candidates never consume their own team's capacity.
type TeamAdmission struct {
	index  *domain.TeamIndex
	limit  int
	active map[domain.SessionID]int
}

// NewTeamAdmission builds the occupancy snapshot for one project. A non-positive
// limit means unlimited and skips counting.
func NewTeamAdmission(records []domain.SessionRecord, limit int) *TeamAdmission {
	a := &TeamAdmission{index: domain.NewTeamIndex(records), limit: limit, active: map[domain.SessionID]int{}}
	if limit <= 0 {
		return a
	}
	for _, rec := range records {
		if rec.IsTerminated || rec.Kind != domain.KindWorker || !rec.SchedulingState.CountsAsActive() {
			continue
		}
		if root, ok := a.index.TeamRoot(rec.ID); ok {
			a.active[root]++
		}
	}
	return a
}

// Blocked reports whether admitting id would exceed its team's limit. A session
// outside any team (standalone, desktop New Task, cloud task, DAG node without
// an orchestrator ancestor) is never team-limited.
func (a *TeamAdmission) Blocked(id domain.SessionID) bool {
	if a == nil || a.limit <= 0 {
		return false
	}
	root, ok := a.index.TeamRoot(id)
	if !ok {
		return false
	}
	return a.active[root] >= a.limit
}

// RecordAdmission accounts for a session this pass just admitted.
func (a *TeamAdmission) RecordAdmission(id domain.SessionID) {
	if a == nil || a.limit <= 0 {
		return
	}
	if root, ok := a.index.TeamRoot(id); ok {
		a.active[root]++
	}
}

func (s *TeamService) projectIndexRecords(ctx context.Context, projectID domain.ProjectID) ([]domain.SessionRecord, error) {
	if projectID == "" {
		return nil, nil
	}
	records, err := s.sessions.ListSessions(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("list sessions for project %s: %w", projectID, err)
	}
	return records, nil
}
