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

// TeamService derives teams from the durable spawn-parent tree.
//
// MaxActiveWorkers is the effective concurrent-active-worker limit for every
// team. Zero means unlimited. The lead is never counted and no derivation here
// launches, pauses, or kills a session.
type TeamService struct {
	sessions         SessionReader
	MaxActiveWorkers int
}

// NewTeamService builds a derivation service with the product default limit.
func NewTeamService(sessions SessionReader) *TeamService {
	return &TeamService{sessions: sessions, MaxActiveWorkers: domain.DefaultMaxActivePerTeam}
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
	team, ok := domain.NewTeamIndex(records).TeamFor(rec.ID, s.MaxActiveWorkers)
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
	team, ok := domain.NewTeamIndex(records).Team(rec.ID, s.MaxActiveWorkers)
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
	return domain.NewTeamIndex(records).Teams(s.MaxActiveWorkers), nil
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
