// Package dagrun owns atomic DAG-run submission and its read model. A run is a
// coordinator, not a session: it groups parentless worker sessions and never
// receives messages or a terminal of its own.
//
// The graph is validated in full before any row exists, and the store persists
// the run, every node, and every in-run edge in one transaction. A rejected
// submission therefore leaves no partial graph for the scheduler to admit.
package dagrun

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// Store is the durable DAG surface. Node creation is one transaction; the run
// status writer is a compare-and-set so a terminal run never moves again.
type Store interface {
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
	CountActiveProjectSessions(ctx context.Context, projectID domain.ProjectID) (int64, error)
	CreateDagRunWithNodes(ctx context.Context, run domain.DagRun, nodes []domain.DagNodeSeed) (domain.DagRun, []domain.SessionRecord, error)
	SelectDagRunByID(ctx context.Context, id domain.DagRunID) (domain.DagRun, bool, error)
	SelectDagRunsByProject(ctx context.Context, projectID domain.ProjectID) ([]domain.DagRun, error)
	UpdateDagRunStatus(ctx context.Context, id domain.DagRunID, status domain.DagRunStatus, completedAt *time.Time) (bool, error)
	CancelDagRun(ctx context.Context, id domain.DagRunID, at time.Time) (bool, error)
	CancelDagRunNodes(ctx context.Context, id domain.DagRunID, at time.Time) (int64, error)
	ListSessionsByDagRun(ctx context.Context, id domain.DagRunID) ([]domain.SessionRecord, error)
	ListSessionsByDagRunIDs(ctx context.Context, ids []domain.DagRunID) (map[domain.DagRunID][]domain.SessionRecord, error)
	ListSessionDependenciesForSessions(ctx context.Context, ids []domain.SessionID) (map[domain.SessionID][]domain.SessionDependency, error)
}

// Snapshotter derives a node's delivery facts at read time. The session service
// implements it from the same Kanban derivation the board renders.
type Snapshotter interface {
	DependencySnapshots(ctx context.Context, ids []domain.SessionID) (map[domain.SessionID]domain.DependencySnapshot, error)
}

// Planner builds the persisted node records for a validated graph. The session
// manager implements it; it validates each node's harness and builds the queued
// spawn request without writing.
type Planner interface {
	DagNodeSeeds(ctx context.Context, projectID domain.ProjectID, nodes []domain.DagNodeSpec) ([]domain.DagNodeSeed, error)
}

// Waker nudges the dependency scheduler after a submission or cancellation.
type Waker interface {
	Notify(projectID domain.ProjectID)
}

// Terminator kills an active node when a cancellation asks to include active
// work. It is optional: without it, active nodes keep running.
type Terminator interface {
	Kill(ctx context.Context, id domain.SessionID) (bool, error)
}

// Deps wires the service. Store, Snapshots, and Planner are required.
type Deps struct {
	Store     Store
	Snapshots Snapshotter
	Planner   Planner
	Waker     Waker
	Sessions  Terminator
	Clock     func() time.Time
	NewID     func() domain.DagRunID
}

// Service submits, reads, and cancels DAG runs.
type Service struct {
	store     Store
	snapshots Snapshotter
	planner   Planner
	waker     Waker
	sessions  Terminator
	clock     func() time.Time
	newID     func() domain.DagRunID
}

// New builds a DAG run service.
func New(d Deps) *Service {
	clock := d.Clock
	if clock == nil {
		clock = time.Now
	}
	newID := d.NewID
	if newID == nil {
		newID = func() domain.DagRunID { return domain.DagRunID("dag-" + uuid.NewString()) }
	}
	return &Service{
		store:     d.Store,
		snapshots: d.Snapshots,
		planner:   d.Planner,
		waker:     d.Waker,
		sessions:  d.Sessions,
		clock:     clock,
		newID:     newID,
	}
}

// Submit validates a run and persists it atomically. Nodes are created
// non-admitted: a root is queued unless the project limit is already full, and
// a dependent node is blocked on its in-run dependencies.
func (s *Service) Submit(ctx context.Context, projectID domain.ProjectID, spec domain.DagRunSpec) (domain.DagRunView, error) {
	if projectID == "" {
		return domain.DagRunView{}, apierr.Invalid("PROJECT_ID_REQUIRED", "projectId is required", nil)
	}
	project, found, err := s.store.GetProject(ctx, string(projectID))
	if err != nil {
		return domain.DagRunView{}, fmt.Errorf("submit dag run: %w", err)
	}
	if !found {
		return domain.DagRunView{}, apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	}
	ordered, err := validateSpec(spec, project.Config.Scheduling.DagNodeLimit())
	if err != nil {
		return domain.DagRunView{}, err
	}
	seeds, err := s.planner.DagNodeSeeds(ctx, projectID, ordered)
	if err != nil {
		return domain.DagRunView{}, dagPlannerError(err)
	}
	active, err := s.store.CountActiveProjectSessions(ctx, projectID)
	if err != nil {
		return domain.DagRunView{}, fmt.Errorf("submit dag run: %w", err)
	}
	now := s.clock()
	applyInitialScheduling(seeds, project.Config.Scheduling, active)
	run := domain.DagRun{
		ID:        s.newID(),
		ProjectID: projectID,
		Name:      strings.TrimSpace(spec.Name),
		Status:    domain.DagRunPending,
		CreatedAt: now,
	}
	created, nodes, err := s.store.CreateDagRunWithNodes(ctx, run, seeds)
	if err != nil {
		return domain.DagRunView{}, fmt.Errorf("submit dag run: %w", err)
	}
	if s.waker != nil {
		s.waker.Notify(projectID)
	}
	return s.viewForRun(ctx, created, nodes)
}

// Get returns one run with its derived status and nodes. The derived status is
// persisted when it moves a non-terminal run, so listing stays truthful
// without a separate reconcile pass.
func (s *Service) Get(ctx context.Context, id domain.DagRunID) (domain.DagRunView, error) {
	run, found, err := s.store.SelectDagRunByID(ctx, id)
	if err != nil {
		return domain.DagRunView{}, fmt.Errorf("get dag run: %w", err)
	}
	if !found {
		return domain.DagRunView{}, apierr.NotFound("DAG_RUN_NOT_FOUND", "Unknown DAG run")
	}
	nodes, err := s.store.ListSessionsByDagRun(ctx, id)
	if err != nil {
		return domain.DagRunView{}, fmt.Errorf("get dag run: %w", err)
	}
	view, err := s.viewForRun(ctx, run, nodes)
	if err != nil {
		return domain.DagRunView{}, err
	}
	s.persistDerivedStatus(ctx, run, view.Status)
	return view, nil
}

// List returns one project's runs newest first, each with its derived status
// and counts.
func (s *Service) List(ctx context.Context, projectID domain.ProjectID) ([]domain.DagRunView, error) {
	runs, err := s.store.SelectDagRunsByProject(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("list dag runs: %w", err)
	}
	if len(runs) == 0 {
		return []domain.DagRunView{}, nil
	}
	ids := make([]domain.DagRunID, 0, len(runs))
	for _, run := range runs {
		ids = append(ids, run.ID)
	}
	nodesByRun, err := s.store.ListSessionsByDagRunIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list dag runs: %w", err)
	}
	// One dependency and snapshot read covers every node in the page.
	var allNodes []domain.SessionRecord
	for _, run := range runs {
		allNodes = append(allNodes, nodesByRun[run.ID]...)
	}
	edgesBySession, snapshots, err := s.nodeFacts(ctx, allNodes)
	if err != nil {
		return nil, err
	}
	out := make([]domain.DagRunView, 0, len(runs))
	for _, run := range runs {
		views := buildNodeViews(nodesByRun[run.ID], edgesBySession, snapshots)
		view := domain.NewDagRunView(run, views)
		s.persistDerivedStatus(ctx, run, view.Status)
		out = append(out, view)
	}
	return out, nil
}

// Cancel marks a run cancelled and cancels its queued and blocked nodes.
// Active nodes keep running unless includeActive is set. Their later
// completion never clears the cancelled status.
func (s *Service) Cancel(ctx context.Context, id domain.DagRunID, includeActive bool) (domain.DagRunView, error) {
	run, found, err := s.store.SelectDagRunByID(ctx, id)
	if err != nil {
		return domain.DagRunView{}, fmt.Errorf("cancel dag run: %w", err)
	}
	if !found {
		return domain.DagRunView{}, apierr.NotFound("DAG_RUN_NOT_FOUND", "Unknown DAG run")
	}
	if run.Status == domain.DagRunCompleted || run.Status == domain.DagRunFailed {
		return domain.DagRunView{}, apierr.Conflict("DAG_RUN_NOT_CANCELLABLE", "This DAG run already finished", nil)
	}
	at := s.clock()
	if _, err := s.store.CancelDagRun(ctx, id, at); err != nil {
		return domain.DagRunView{}, fmt.Errorf("cancel dag run: %w", err)
	}
	if _, err := s.store.CancelDagRunNodes(ctx, id, at); err != nil {
		return domain.DagRunView{}, fmt.Errorf("cancel dag run: %w", err)
	}
	if includeActive && s.sessions != nil {
		nodes, err := s.store.ListSessionsByDagRun(ctx, id)
		if err != nil {
			return domain.DagRunView{}, fmt.Errorf("cancel dag run: %w", err)
		}
		for _, node := range nodes {
			state := node.SchedulingState.WithDefault()
			if node.IsTerminated || state.IsPreLaunch() || state == domain.SchedulingCancelled {
				continue
			}
			if _, err := s.sessions.Kill(ctx, node.ID); err != nil {
				return domain.DagRunView{}, fmt.Errorf("cancel dag run %s: kill %s: %w", id, node.ID, err)
			}
		}
	}
	if s.waker != nil {
		s.waker.Notify(run.ProjectID)
	}
	return s.Get(ctx, id)
}

// persistDerivedStatus writes a status that moved while the run was still
// non-terminal. It is best-effort on the read path: a failure leaves the
// derived value in the response and only delays the stored summary.
func (s *Service) persistDerivedStatus(ctx context.Context, run domain.DagRun, status domain.DagRunStatus) {
	if run.Status.Terminal() || run.Status == status {
		return
	}
	var completedAt *time.Time
	if status.Terminal() {
		at := s.clock()
		completedAt = &at
	}
	_, _ = s.store.UpdateDagRunStatus(ctx, run.ID, status, completedAt)
}

// viewForRun builds the read model from already-loaded session rows.
func (s *Service) viewForRun(ctx context.Context, run domain.DagRun, nodes []domain.SessionRecord) (domain.DagRunView, error) {
	edgesBySession, snapshots, err := s.nodeFacts(ctx, nodes)
	if err != nil {
		return domain.DagRunView{}, err
	}
	return domain.NewDagRunView(run, buildNodeViews(nodes, edgesBySession, snapshots)), nil
}

// nodeFacts batches the dependency edges and delivery snapshots for a set of
// node sessions.
func (s *Service) nodeFacts(ctx context.Context, nodes []domain.SessionRecord) (map[domain.SessionID][]domain.SessionDependency, map[domain.SessionID]domain.DependencySnapshot, error) {
	if len(nodes) == 0 {
		return nil, nil, nil
	}
	ids := make([]domain.SessionID, 0, len(nodes))
	for _, node := range nodes {
		ids = append(ids, node.ID)
	}
	edges, err := s.store.ListSessionDependenciesForSessions(ctx, ids)
	if err != nil {
		return nil, nil, fmt.Errorf("dag run node edges: %w", err)
	}
	snapshots, err := s.snapshots.DependencySnapshots(ctx, ids)
	if err != nil {
		return nil, nil, fmt.Errorf("dag run node snapshots: %w", err)
	}
	return edges, snapshots, nil
}

// buildNodeViews derives one view per node. A row without a node id is not part
// of the submitted graph and is skipped.
func buildNodeViews(nodes []domain.SessionRecord, edges map[domain.SessionID][]domain.SessionDependency, snapshots map[domain.SessionID]domain.DependencySnapshot) []domain.DagNodeView {
	nodeIDBySession := make(map[domain.SessionID]domain.DagNodeID, len(nodes))
	for _, node := range nodes {
		if node.DagNodeID != nil {
			nodeIDBySession[node.ID] = *node.DagNodeID
		}
	}
	views := make([]domain.DagNodeView, 0, len(nodes))
	for _, node := range nodes {
		if node.DagNodeID == nil {
			continue
		}
		dependsOn := make([]domain.DagNodeID, 0, len(edges[node.ID]))
		for _, edge := range edges[node.ID] {
			if depID, ok := nodeIDBySession[edge.DependsOnSessionID]; ok {
				dependsOn = append(dependsOn, depID)
			}
		}
		name := node.DisplayName
		if name == "" {
			name = string(*node.DagNodeID)
		}
		views = append(views, domain.DagNodeView{
			NodeID:        *node.DagNodeID,
			SessionID:     node.ID,
			Name:          name,
			Status:        domain.DeriveDagNodeStatus(node, snapshots[node.ID]),
			BlockedReason: node.BlockedReason,
			DependsOn:     dependsOn,
			CreatedAt:     node.CreatedAt,
		})
	}
	return views
}

// applyInitialScheduling marks every node non-admitted. A root is queued while
// the project has a free slot and blocked by the limit after that; a dependent
// node starts blocked on its in-run dependencies, which cannot be satisfied
// before the run exists.
func applyInitialScheduling(seeds []domain.DagNodeSeed, config domain.SchedulingConfig, active int64) {
	remaining := int64(-1)
	if config.MaxActiveSessions > 0 {
		remaining = int64(config.MaxActiveSessions) - active
		if remaining < 0 {
			remaining = 0
		}
	}
	for i := range seeds {
		seed := &seeds[i]
		if len(seed.Dependencies) > 0 {
			seed.Record.SchedulingState = domain.SchedulingBlocked
			seed.Record.BlockedReason = string(domain.BlockedDependencyPending)
			continue
		}
		if remaining == 0 {
			seed.Record.SchedulingState = domain.SchedulingBlocked
			seed.Record.BlockedReason = string(domain.BlockedProjectLimit)
			continue
		}
		seed.Record.SchedulingState = domain.SchedulingQueued
		seed.Record.BlockedReason = ""
		if remaining > 0 {
			remaining--
		}
	}
}

// dagPlannerError maps a node-planning failure to the API envelope. A missing
// harness is a caller-fixable submission error; anything else stays a server
// error.
func dagPlannerError(err error) error {
	message := err.Error()
	if strings.Contains(message, "agent harness required") {
		return apierr.Invalid("DAG_NODE_HARNESS_INVALID", message, nil)
	}
	if strings.Contains(message, "unknown agent harness") {
		return apierr.Invalid("DAG_NODE_HARNESS_INVALID", message, nil)
	}
	return fmt.Errorf("submit dag run: %w", err)
}

// validateSpec normalizes a submission and returns its nodes in dependency
// order. Nothing is persisted here: every check fails before the transaction.
func validateSpec(spec domain.DagRunSpec, limit int) ([]domain.DagNodeSpec, error) {
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		return nil, apierr.Invalid("DAG_RUN_NAME_INVALID", "A DAG run name is required", nil)
	}
	if utf8.RuneCountInString(name) > domain.MaxDagRunNameLength {
		return nil, apierr.Invalid("DAG_RUN_NAME_INVALID",
			fmt.Sprintf("A DAG run name must be %d characters or fewer", domain.MaxDagRunNameLength), nil)
	}
	if len(spec.Nodes) == 0 {
		return nil, apierr.Invalid("DAG_RUN_EMPTY", "A DAG run needs at least one node", nil)
	}
	if len(spec.Nodes) > limit {
		return nil, apierr.Invalid("DAG_RUN_LIMIT",
			fmt.Sprintf("A DAG run may contain at most %d nodes", limit), nil)
	}

	nodes := make([]domain.DagNodeSpec, len(spec.Nodes))
	index := make(map[domain.DagNodeID]int, len(spec.Nodes))
	for i, raw := range spec.Nodes {
		node, err := normalizeNode(raw)
		if err != nil {
			return nil, err
		}
		if _, duplicate := index[node.ID]; duplicate {
			return nil, apierr.Invalid("DAG_NODE_DUPLICATE",
				fmt.Sprintf("Node id %q appears more than once", node.ID), nil)
		}
		index[node.ID] = i
		nodes[i] = node
	}
	for i := range nodes {
		seen := make(map[domain.DagNodeID]bool, len(nodes[i].DependsOn))
		for j, dep := range nodes[i].DependsOn {
			if dep == nodes[i].ID {
				return nil, apierr.Invalid("DAG_NODE_SELF_DEPENDENCY",
					fmt.Sprintf("Node %q depends on itself", nodes[i].ID), nil)
			}
			if _, ok := index[dep]; !ok {
				return nil, apierr.Invalid("DAG_NODE_DEPENDENCY_UNKNOWN",
					fmt.Sprintf("Node %q depends on unknown node %q", nodes[i].ID, dep), nil)
			}
			if seen[dep] {
				return nil, apierr.Invalid("DAG_NODE_DUPLICATE",
					fmt.Sprintf("Node %q lists dependency %q more than once", nodes[i].ID, dep), nil)
			}
			seen[dep] = true
			nodes[i].DependsOn[j] = dep
		}
	}
	return topologicalOrder(nodes, index)
}

// normalizeNode trims and validates one submitted node without looking at the
// rest of the graph.
func normalizeNode(raw domain.DagNodeSpec) (domain.DagNodeSpec, error) {
	id := domain.DagNodeID(strings.TrimSpace(string(raw.ID)))
	if id == "" || strings.ContainsAny(string(id), " \t\r\n") {
		return domain.DagNodeSpec{}, apierr.Invalid("DAG_NODE_ID_INVALID", "Every DAG node needs an id without whitespace", nil)
	}
	if utf8.RuneCountInString(string(id)) > domain.MaxDagNodeIDLength {
		return domain.DagNodeSpec{}, apierr.Invalid("DAG_NODE_ID_INVALID",
			fmt.Sprintf("A node id must be %d characters or fewer", domain.MaxDagNodeIDLength), nil)
	}
	prompt := strings.TrimSpace(raw.Prompt)
	if prompt == "" {
		return domain.DagNodeSpec{}, apierr.Invalid("DAG_NODE_PROMPT_INVALID",
			fmt.Sprintf("Node %q needs a prompt", id), nil)
	}
	if utf8.RuneCountInString(prompt) > domain.MaxDagNodePromptLength {
		return domain.DagNodeSpec{}, apierr.Invalid("DAG_NODE_PROMPT_INVALID",
			fmt.Sprintf("Node %q prompt must be 16 KiB or fewer", id), nil)
	}
	name := strings.TrimSpace(raw.Name)
	if utf8.RuneCountInString(name) > domain.MaxDagNodeNameLength {
		return domain.DagNodeSpec{}, apierr.Invalid("DAG_NODE_NAME_INVALID",
			fmt.Sprintf("Node %q name must be %d characters or fewer", id, domain.MaxDagNodeNameLength), nil)
	}
	if !raw.RequestedOutput.Valid() {
		return domain.DagNodeSpec{}, apierr.Invalid("DAG_NODE_STATE_INVALID",
			fmt.Sprintf("Node %q requests an unknown output", id), nil)
	}
	if raw.RequiredState != "" && !raw.RequiredState.Valid() {
		return domain.DagNodeSpec{}, apierr.Invalid("DAG_NODE_STATE_INVALID",
			fmt.Sprintf("Node %q has an unknown required state", id), nil)
	}
	deps := make([]domain.DagNodeID, 0, len(raw.DependsOn))
	for _, dep := range raw.DependsOn {
		trimmed := domain.DagNodeID(strings.TrimSpace(string(dep)))
		if trimmed == "" {
			return domain.DagNodeSpec{}, apierr.Invalid("DAG_NODE_DEPENDENCY_UNKNOWN",
				fmt.Sprintf("Node %q lists an empty dependency", id), nil)
		}
		deps = append(deps, trimmed)
	}
	return domain.DagNodeSpec{
		ID:              id,
		Name:            name,
		Prompt:          prompt,
		Harness:         raw.Harness,
		DependsOn:       deps,
		RequiredState:   raw.RequiredState,
		RequestedOutput: raw.RequestedOutput,
	}, nil
}

// topologicalOrder orders nodes so every dependency precedes its dependent,
// preserving submission order among ready nodes. A cycle is rejected with a
// precise error instead of blocking every node on a dependency that can never
// resolve.
func topologicalOrder(nodes []domain.DagNodeSpec, index map[domain.DagNodeID]int) ([]domain.DagNodeSpec, error) {
	indegree := make([]int, len(nodes))
	dependents := make([][]int, len(nodes))
	for i := range nodes {
		indegree[i] = len(nodes[i].DependsOn)
		for _, dep := range nodes[i].DependsOn {
			dependents[index[dep]] = append(dependents[index[dep]], i)
		}
	}
	queue := make([]int, 0, len(nodes))
	for i := range nodes {
		if indegree[i] == 0 {
			queue = append(queue, i)
		}
	}
	ordered := make([]domain.DagNodeSpec, 0, len(nodes))
	for len(queue) > 0 {
		i := queue[0]
		queue = queue[1:]
		ordered = append(ordered, nodes[i])
		for _, next := range dependents[i] {
			indegree[next]--
			if indegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	if len(ordered) != len(nodes) {
		return nil, apierr.Invalid("DAG_NODE_CYCLE", "A DAG run cannot contain a dependency cycle", nil)
	}
	return ordered, nil
}
