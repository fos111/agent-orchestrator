// Package scheduling owns dependency admission for queued sessions. A session
// created with dependencies is persisted before it owns a workspace; this
// package re-evaluates its edges, admits it when the project limits allow, and
// launches it outside the decision.
//
// The policy is deliberately narrow: the store owns the durable compare-and-set
// transitions, the session service owns the derived delivery facts, and this
// package only decides. Everything it writes is a narrow scheduling update, so
// a full-row session write elsewhere can never resurrect a stale decision.
package scheduling

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/sentryobs"
)

const (
	// DefaultInterval is the periodic sweep cadence. It covers missed events
	// and PR polling gaps; it is not the primary admission trigger.
	DefaultInterval = 15 * time.Second
	// DefaultDebounce coalesces a burst of facts about one project into one
	// reconcile, so a PR poll or a fan-out kill cannot cause a reconcile storm.
	DefaultDebounce = 100 * time.Millisecond
	// reconcileTimeout bounds one event-driven reconcile, which includes
	// materializing a workspace and running project post-create commands.
	reconcileTimeout     = 10 * time.Minute
	bootReconcileTimeout = 10 * time.Minute
)

// Store is the durable scheduling surface. Every state transition is a
// compare-and-set, so a kill, a force admit, or a second daemon can win without
// this package writing over it.
type Store interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
	ListProjects(ctx context.Context) ([]domain.ProjectRecord, error)
	ListSchedulingCandidateIDs(ctx context.Context, projectID domain.ProjectID) ([]domain.SessionID, error)
	ListSchedulingProjectIDs(ctx context.Context) ([]domain.ProjectID, error)
	ListSessionDependencies(ctx context.Context, id domain.SessionID) ([]domain.SessionDependency, error)
	ListSessionDependenciesForSessions(ctx context.Context, ids []domain.SessionID) (map[domain.SessionID][]domain.SessionDependency, error)
	CountActiveProjectSessions(ctx context.Context, projectID domain.ProjectID) (int64, error)
	SetSessionSchedulingState(ctx context.Context, id domain.SessionID, expected, next domain.SchedulingState, reason domain.BlockedReason, now time.Time) (bool, error)
	AdmitSession(ctx context.Context, id domain.SessionID, at time.Time, override bool) (bool, error)
	SetSessionAdmissionOverride(ctx context.Context, id domain.SessionID, now time.Time) (bool, error)
	MarkSessionSchedulingLaunched(ctx context.Context, id domain.SessionID, now time.Time) (bool, error)
	SetSessionLaunchFailed(ctx context.Context, id domain.SessionID, provisionError string, now time.Time) (bool, error)
	ListAdmittedSessionsWithoutWorkspace(ctx context.Context, projectID domain.ProjectID) ([]domain.SessionID, error)
	ListAdmittedSessionsWithWorkspace(ctx context.Context, projectID domain.ProjectID) ([]domain.SessionID, error)
}

// Launcher materializes an admitted reservation. The manager owns the
// workspace, controller, and post-create steps; this package only calls it.
type Launcher interface {
	LaunchSpawn(ctx context.Context, id domain.SessionID) (domain.SessionRecord, error)
}

// Snapshotter derives a dependency's delivery facts at read time. The session
// service implements it from the same Kanban derivation the board renders.
type Snapshotter interface {
	DependencySnapshots(ctx context.Context, ids []domain.SessionID) (map[domain.SessionID]domain.DependencySnapshot, error)
}

// Deps wires the scheduler. Store, Launcher, and Snapshots are required.
type Deps struct {
	Store     Store
	Launcher  Launcher
	Snapshots Snapshotter
	Clock     func() time.Time
	Logger    *slog.Logger
	// BackgroundContext owns event-driven reconciles; the daemon cancels it on
	// shutdown so a debounced wake cannot outlive the process it serves.
	BackgroundContext context.Context
	// Interval and Debounce default to DefaultInterval and DefaultDebounce.
	Interval time.Duration
	Debounce time.Duration
}

// Service is the dependency scheduler. It is safe for concurrent use; work for
// one project is serialized, work for different projects may overlap.
type Service struct {
	store      Store
	launcher   Launcher
	snapshots  Snapshotter
	clock      func() time.Time
	logger     *slog.Logger
	background context.Context
	interval   time.Duration
	debounce   time.Duration

	projectLocksMu sync.Mutex
	projectLocks   map[domain.ProjectID]*sync.Mutex

	timersMu sync.Mutex
	timers   map[domain.ProjectID]*time.Timer
	closed   bool
}

// New builds a scheduler.
func New(d Deps) *Service {
	clock := d.Clock
	if clock == nil {
		clock = time.Now
	}
	interval := d.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	debounce := d.Debounce
	if debounce <= 0 {
		debounce = DefaultDebounce
	}
	return &Service{
		store:        d.Store,
		launcher:     d.Launcher,
		snapshots:    d.Snapshots,
		clock:        clock,
		logger:       d.Logger,
		background:   d.BackgroundContext,
		interval:     interval,
		debounce:     debounce,
		projectLocks: make(map[domain.ProjectID]*sync.Mutex),
		timers:       make(map[domain.ProjectID]*time.Timer),
	}
}

// Result reports what one reconcile pass decided. It exists for tests and
// diagnostics; callers do not need to act on it.
type Result struct {
	ProjectID domain.ProjectID
	Evaluated int
	Admitted  []domain.SessionID
	Launched  []domain.SessionID
	Failed    []domain.SessionID
}

// Reconcile re-evaluates one project's queued and blocked sessions, admits the
// oldest ready ones within the project limit, and launches every admitted
// session outside the decision.
func (s *Service) Reconcile(ctx context.Context, projectID domain.ProjectID) (Result, error) {
	if projectID == "" {
		return Result{}, fmt.Errorf("reconcile: a project id is required")
	}
	unlock := s.lockProject(projectID)
	defer unlock()
	return s.reconcileLocked(ctx, projectID)
}

func (s *Service) reconcileLocked(ctx context.Context, projectID domain.ProjectID) (Result, error) {
	res := Result{ProjectID: projectID}
	config, err := s.schedulingConfig(ctx, projectID)
	if err != nil {
		return res, err
	}
	active, err := s.store.CountActiveProjectSessions(ctx, projectID)
	if err != nil {
		return res, err
	}
	limit := int64(config.MaxActiveSessions)
	if err := s.evaluate(ctx, projectID, limit > 0 && active >= limit, &res); err != nil {
		return res, err
	}
	if err := s.admit(ctx, projectID, limit, &active, &res); err != nil {
		return res, err
	}
	s.launchAdmitted(ctx, &res)
	if err := s.launchUnlaunchedAdmitted(ctx, projectID, &res); err != nil {
		return res, err
	}
	if err := s.finishLaunchedAdmitted(ctx, projectID); err != nil {
		return res, err
	}
	return res, nil
}

// evaluate moves each candidate to the state its edges imply. A candidate
// already in the implied state is left alone, so a stable queue produces no
// writes and no event churn.
func (s *Service) evaluate(ctx context.Context, projectID domain.ProjectID, projectFull bool, res *Result) error {
	ids, err := s.store.ListSchedulingCandidateIDs(ctx, projectID)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	records := make(map[domain.SessionID]domain.SessionRecord, len(ids))
	for _, id := range ids {
		rec, found, err := s.store.GetSession(ctx, id)
		if err != nil {
			return err
		}
		if !found || rec.IsTerminated || !rec.SchedulingState.IsPreLaunch() {
			continue
		}
		records[id] = rec
	}
	if len(records) == 0 {
		return nil
	}
	scoped := make([]domain.SessionID, 0, len(records))
	for _, id := range ids {
		if _, ok := records[id]; ok {
			scoped = append(scoped, id)
		}
	}
	edgesBySession, err := s.store.ListSessionDependenciesForSessions(ctx, scoped)
	if err != nil {
		return err
	}
	snapshots, err := s.snapshots.DependencySnapshots(ctx, dependencyIDs(edgesBySession))
	if err != nil {
		return err
	}
	now := s.clock()
	for _, id := range scoped {
		rec := records[id]
		res.Evaluated++
		next, reason := decide(edgesBySession[id], snapshots)
		if next == domain.SchedulingQueued && projectFull {
			// Dependencies are satisfied but no slot is free. Recording the
			// limit here keeps the queue from flapping queued/blocked on every
			// tick while the project is full.
			next, reason = domain.SchedulingBlocked, domain.BlockedProjectLimit
		}
		current := rec.SchedulingState.WithDefault()
		if current == next && (next != domain.SchedulingBlocked || domain.BlockedReason(rec.BlockedReason) == reason) {
			continue
		}
		if _, err := s.store.SetSessionSchedulingState(ctx, id, current, next, reason, now); err != nil {
			return err
		}
	}
	return nil
}

// admit records admission with a compare-and-set in FIFO order. Sessions left
// over the limit go back to blocked(project_limit) rather than waiting in the
// queue, so the reason is visible.
func (s *Service) admit(ctx context.Context, projectID domain.ProjectID, limit int64, active *int64, res *Result) error {
	ids, err := s.store.ListSchedulingCandidateIDs(ctx, projectID)
	if err != nil {
		return err
	}
	now := s.clock()
	for _, id := range ids {
		if limit > 0 && *active >= limit {
			if _, err := s.store.SetSessionSchedulingState(ctx, id, domain.SchedulingQueued, domain.SchedulingBlocked, domain.BlockedProjectLimit, now); err != nil {
				return err
			}
			continue
		}
		rec, found, err := s.store.GetSession(ctx, id)
		if err != nil {
			return err
		}
		if !found || rec.IsTerminated || rec.SchedulingState.WithDefault() != domain.SchedulingQueued {
			continue
		}
		admitted, err := s.store.AdmitSession(ctx, id, now, false)
		if err != nil {
			return err
		}
		if !admitted {
			// A kill or a forced admit won the race; the next pass sees the
			// resulting row.
			continue
		}
		*active++
		res.Admitted = append(res.Admitted, id)
	}
	return nil
}

// launchAdmitted materializes each admission. It runs after the decision, and
// a failure becomes a durable blocked(launch_failed) row instead of a retry.
func (s *Service) launchAdmitted(ctx context.Context, res *Result) {
	for _, id := range res.Admitted {
		if ctx.Err() != nil {
			return
		}
		s.launch(ctx, id, res)
	}
}

// finishLaunchedAdmitted recovers the other half of an interrupted launch: the
// workspace or runtime exists, but the process died before recording that the
// admitted row became an ordinary active session. Marking it here is safe and
// idempotent; relaunching it would build a second workspace.
func (s *Service) finishLaunchedAdmitted(ctx context.Context, projectID domain.ProjectID) error {
	ids, err := s.store.ListAdmittedSessionsWithWorkspace(ctx, projectID)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := s.store.MarkSessionSchedulingLaunched(ctx, id, s.clock()); err != nil {
			return err
		}
	}
	return nil
}

// launchUnlaunchedAdmitted recovers a decision an earlier process committed but
// never launched: the row is admitted, owns no workspace and no runtime, and is
// therefore safe to relaunch under the launch guard.
func (s *Service) launchUnlaunchedAdmitted(ctx context.Context, projectID domain.ProjectID, res *Result) error {
	ids, err := s.store.ListAdmittedSessionsWithoutWorkspace(ctx, projectID)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return nil
		}
		if containsSession(res.Admitted, id) {
			continue
		}
		s.launch(ctx, id, res)
	}
	return nil
}

func (s *Service) launch(ctx context.Context, id domain.SessionID, res *Result) {
	now := s.clock()
	rec, err := s.launcher.LaunchSpawn(ctx, id)
	if err != nil {
		if _, failErr := s.store.SetSessionLaunchFailed(ctx, id, err.Error(), now); failErr != nil {
			s.logError("record scheduling launch failure", "session", id, "err", failErr)
		}
		res.Failed = append(res.Failed, id)
		s.logError("scheduled session launch failed", "session", id, "err", err)
		return
	}
	if _, err := s.store.MarkSessionSchedulingLaunched(ctx, rec.ID, now); err != nil {
		s.logError("mark scheduled session launched", "session", rec.ID, "err", err)
		return
	}
	res.Launched = append(res.Launched, rec.ID)
}

// Admit decides one pre-launch session immediately. A non-forced admit
// re-evaluates dependencies and limits; a forced admit bypasses both and
// records the override.
func (s *Service) Admit(ctx context.Context, id domain.SessionID, force bool) (domain.SessionRecord, error) {
	rec, found, err := s.store.GetSession(ctx, id)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("admit %s: %w", id, err)
	}
	if !found {
		return domain.SessionRecord{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	unlock := s.lockProject(rec.ProjectID)
	defer unlock()
	if rec.IsTerminated || rec.SchedulingState.WithDefault() == domain.SchedulingCancelled {
		return rec, apierr.Conflict("SESSION_NOT_ADMITTABLE", "This session is already terminated", nil)
	}
	if !rec.SchedulingState.IsPreLaunch() {
		// Already admitted or running: admission is idempotent.
		return rec, nil
	}
	now := s.clock()
	if force {
		if _, err := s.store.SetSessionAdmissionOverride(ctx, id, now); err != nil {
			return rec, fmt.Errorf("admit %s: %w", id, err)
		}
		admitted, err := s.store.AdmitSession(ctx, id, now, true)
		if err != nil {
			return rec, fmt.Errorf("admit %s: %w", id, err)
		}
		if admitted {
			res := Result{ProjectID: rec.ProjectID, Admitted: []domain.SessionID{id}}
			s.launchAdmitted(ctx, &res)
		}
		return s.reload(ctx, id)
	}
	edges, err := s.store.ListSessionDependencies(ctx, id)
	if err != nil {
		return rec, fmt.Errorf("admit %s: %w", id, err)
	}
	snapshots, err := s.snapshots.DependencySnapshots(ctx, dependencyIDs(map[domain.SessionID][]domain.SessionDependency{id: edges}))
	if err != nil {
		return rec, err
	}
	next, reason := decide(edges, snapshots)
	current := rec.SchedulingState.WithDefault()
	if next != domain.SchedulingQueued {
		if current != next || domain.BlockedReason(rec.BlockedReason) != reason {
			if _, err := s.store.SetSessionSchedulingState(ctx, id, current, domain.SchedulingBlocked, reason, now); err != nil {
				return rec, fmt.Errorf("admit %s: %w", id, err)
			}
		}
		return s.reloadWithAdmitError(ctx, id, reason)
	}
	if current != next {
		if _, err := s.store.SetSessionSchedulingState(ctx, id, current, next, "", now); err != nil {
			return rec, fmt.Errorf("admit %s: %w", id, err)
		}
	}
	if _, err := s.reconcileLocked(ctx, rec.ProjectID); err != nil {
		return rec, err
	}
	return s.reload(ctx, id)
}

func (s *Service) reload(ctx context.Context, id domain.SessionID) (domain.SessionRecord, error) {
	rec, found, err := s.store.GetSession(ctx, id)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("reload %s: %w", id, err)
	}
	if !found {
		return domain.SessionRecord{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	return rec, nil
}

func (s *Service) reloadWithAdmitError(ctx context.Context, id domain.SessionID, reason domain.BlockedReason) (domain.SessionRecord, error) {
	rec, err := s.reload(ctx, id)
	if err != nil {
		return domain.SessionRecord{}, err
	}
	return rec, apierr.Conflict("SESSION_NOT_ADMITTABLE",
		fmt.Sprintf("This session is blocked: %s", reason), nil)
}

// Notify requests a debounced reconcile of one project. The first call starts
// the window; calls inside it fold into the same pass. It never blocks the
// caller, so a kill or a completion never waits on a workspace launch.
func (s *Service) Notify(projectID domain.ProjectID) {
	if s == nil || projectID == "" {
		return
	}
	s.timersMu.Lock()
	defer s.timersMu.Unlock()
	if s.closed {
		return
	}
	if _, pending := s.timers[projectID]; pending {
		return
	}
	s.timers[projectID] = time.AfterFunc(s.debounce, func() {
		s.timersMu.Lock()
		if s.closed {
			s.timersMu.Unlock()
			return
		}
		delete(s.timers, projectID)
		s.timersMu.Unlock()
		s.reconcileBackground(projectID)
	})
}

func (s *Service) reconcileBackground(projectID domain.ProjectID) {
	parent := s.background
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, reconcileTimeout)
	defer cancel()
	if _, err := s.Reconcile(ctx, projectID); err != nil && !errors.Is(err, context.Canceled) {
		s.logError("scheduler reconcile failed", "project", projectID, "err", err)
	}
}

// Start runs the boot reconcile and the periodic sweep until ctx is done. The
// returned channel closes when the loop has stopped.
func (s *Service) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer sentryobs.RecoverBackground()
		bootCtx, cancel := context.WithTimeout(ctx, bootReconcileTimeout)
		if err := s.Recover(bootCtx); err != nil && !errors.Is(err, context.Canceled) {
			s.logError("scheduler boot reconcile failed", "err", err)
		}
		cancel()
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				s.Close()
				return
			case <-ticker.C:
				s.Sweep(ctx)
			}
		}
	}()
	return done
}

// Recover reconciles every project at daemon start. It covers queued and
// blocked sessions and relaunches an admitted session whose launch never ran.
func (s *Service) Recover(ctx context.Context) error {
	projects, err := s.store.ListProjects(ctx)
	if err != nil {
		return fmt.Errorf("list projects: %w", err)
	}
	for _, project := range projects {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if _, err := s.Reconcile(ctx, domain.ProjectID(project.ID)); err != nil {
			s.logError("scheduler boot reconcile failed", "project", project.ID, "err", err)
		}
	}
	return nil
}

// Sweep reconciles every project that currently has queued or blocked work.
func (s *Service) Sweep(ctx context.Context) {
	ids, err := s.store.ListSchedulingProjectIDs(ctx)
	if err != nil {
		s.logError("list scheduling projects", "err", err)
		return
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		if _, err := s.Reconcile(ctx, id); err != nil {
			s.logError("scheduler sweep failed", "project", id, "err", err)
		}
	}
}

// Close stops pending debounce timers. Reconciles already running finish on
// their own context.
func (s *Service) Close() {
	s.timersMu.Lock()
	defer s.timersMu.Unlock()
	s.closed = true
	for id, timer := range s.timers {
		timer.Stop()
		delete(s.timers, id)
	}
}

func (s *Service) schedulingConfig(ctx context.Context, projectID domain.ProjectID) (domain.SchedulingConfig, error) {
	project, found, err := s.store.GetProject(ctx, string(projectID))
	if err != nil {
		return domain.SchedulingConfig{}, err
	}
	if !found {
		return domain.SchedulingConfig{}, nil
	}
	return project.Config.Scheduling, nil
}

func (s *Service) lockProject(projectID domain.ProjectID) func() {
	s.projectLocksMu.Lock()
	lock, ok := s.projectLocks[projectID]
	if !ok {
		lock = &sync.Mutex{}
		s.projectLocks[projectID] = lock
	}
	s.projectLocksMu.Unlock()
	lock.Lock()
	return lock.Unlock
}

func (s *Service) logError(msg string, args ...any) {
	if s.logger != nil {
		s.logger.Error(msg, args...)
	}
}

func dependencyIDs(edgesBySession map[domain.SessionID][]domain.SessionDependency) []domain.SessionID {
	seen := make(map[domain.SessionID]bool)
	out := make([]domain.SessionID, 0, len(edgesBySession))
	for _, edges := range edgesBySession {
		for _, edge := range edges {
			if seen[edge.DependsOnSessionID] {
				continue
			}
			seen[edge.DependsOnSessionID] = true
			out = append(out, edge.DependsOnSessionID)
		}
	}
	return out
}

func containsSession(ids []domain.SessionID, want domain.SessionID) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}
