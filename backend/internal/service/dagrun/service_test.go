package dagrun

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// fakeStore is the in-memory durable surface. It mirrors the store's contract:
// CreateDagRunWithNodes assigns session ids, stamps the run/node ids, and
// persists every node together.
type fakeStore struct {
	project      domain.ProjectRecord
	projectFound bool
	active       int64
	createCalls  int
	nextSession  int

	runs          map[domain.DagRunID]domain.DagRun
	nodes         map[domain.DagRunID][]domain.SessionRecord
	edges         map[domain.SessionID][]domain.SessionDependency
	statusUpdates []dagStatusUpdate
	cancelledRuns []domain.DagRunID
	cancelledNode int64
}

type dagStatusUpdate struct {
	id     domain.DagRunID
	status domain.DagRunStatus
}

func newFakeStore(project domain.ProjectRecord) *fakeStore {
	return &fakeStore{
		project:      project,
		projectFound: true,
		runs:         map[domain.DagRunID]domain.DagRun{},
		nodes:        map[domain.DagRunID][]domain.SessionRecord{},
		edges:        map[domain.SessionID][]domain.SessionDependency{},
	}
}

func (s *fakeStore) GetProject(context.Context, string) (domain.ProjectRecord, bool, error) {
	return s.project, s.projectFound, nil
}

func (s *fakeStore) CountActiveProjectSessions(context.Context, domain.ProjectID) (int64, error) {
	return s.active, nil
}

func (s *fakeStore) CreateDagRunWithNodes(_ context.Context, run domain.DagRun, seeds []domain.DagNodeSeed) (domain.DagRun, []domain.SessionRecord, error) {
	s.createCalls++
	created := make([]domain.SessionRecord, 0, len(seeds))
	byNode := map[domain.DagNodeID]domain.SessionID{}
	for i, seed := range seeds {
		rec := seed.Record
		s.nextSession++
		rec.ID = domain.SessionID("node-" + itoa(i+1))
		runID, nodeID := run.ID, seed.NodeID
		rec.DagRunID = &runID
		rec.DagNodeID = &nodeID
		byNode[seed.NodeID] = rec.ID
		created = append(created, rec)
	}
	for i, seed := range seeds {
		for _, dep := range seed.Dependencies {
			s.edges[created[i].ID] = append(s.edges[created[i].ID], domain.SessionDependency{
				SessionID:          created[i].ID,
				DependsOnSessionID: byNode[dep.NodeID],
				RequiredState:      dep.RequiredState,
			})
		}
	}
	s.runs[run.ID] = run
	s.nodes[run.ID] = created
	return run, created, nil
}

func (s *fakeStore) SelectDagRunByID(_ context.Context, id domain.DagRunID) (domain.DagRun, bool, error) {
	run, ok := s.runs[id]
	return run, ok, nil
}

func (s *fakeStore) SelectDagRunsByProject(_ context.Context, projectID domain.ProjectID) ([]domain.DagRun, error) {
	out := []domain.DagRun{}
	for _, run := range s.runs {
		if run.ProjectID == projectID {
			out = append(out, run)
		}
	}
	return out, nil
}

func (s *fakeStore) UpdateDagRunStatus(_ context.Context, id domain.DagRunID, status domain.DagRunStatus, completedAt *time.Time) (bool, error) {
	s.statusUpdates = append(s.statusUpdates, dagStatusUpdate{id: id, status: status})
	run := s.runs[id]
	run.Status = status
	if completedAt != nil {
		run.CompletedAt = completedAt
	}
	s.runs[id] = run
	return true, nil
}

func (s *fakeStore) CancelDagRun(_ context.Context, id domain.DagRunID, at time.Time) (bool, error) {
	s.cancelledRuns = append(s.cancelledRuns, id)
	run := s.runs[id]
	run.Status = domain.DagRunCancelled
	run.CancelRequestedAt = &at
	run.CompletedAt = &at
	s.runs[id] = run
	return true, nil
}

func (s *fakeStore) CancelDagRunNodes(_ context.Context, id domain.DagRunID, _ time.Time) (int64, error) {
	var changed int64
	nodes := s.nodes[id]
	for i := range nodes {
		if nodes[i].SchedulingState.IsPreLaunch() {
			nodes[i].SchedulingState = domain.SchedulingCancelled
			nodes[i].IsTerminated = true
			changed++
		}
	}
	s.nodes[id] = nodes
	s.cancelledNode += changed
	return changed, nil
}

func (s *fakeStore) ListSessionsByDagRun(_ context.Context, id domain.DagRunID) ([]domain.SessionRecord, error) {
	return s.nodes[id], nil
}

func (s *fakeStore) ListSessionsByDagRunIDs(_ context.Context, ids []domain.DagRunID) (map[domain.DagRunID][]domain.SessionRecord, error) {
	out := make(map[domain.DagRunID][]domain.SessionRecord, len(ids))
	for _, id := range ids {
		out[id] = s.nodes[id]
	}
	return out, nil
}

func (s *fakeStore) ListSessionDependenciesForSessions(_ context.Context, ids []domain.SessionID) (map[domain.SessionID][]domain.SessionDependency, error) {
	out := make(map[domain.SessionID][]domain.SessionDependency, len(ids))
	for _, id := range ids {
		if edges, ok := s.edges[id]; ok {
			out[id] = edges
		}
	}
	return out, nil
}

type fakePlanner struct{ err error }

func (p fakePlanner) DagNodeSeeds(_ context.Context, projectID domain.ProjectID, nodes []domain.DagNodeSpec) ([]domain.DagNodeSeed, error) {
	if p.err != nil {
		return nil, p.err
	}
	now := time.Unix(1000, 0).UTC()
	seeds := make([]domain.DagNodeSeed, 0, len(nodes))
	for _, node := range nodes {
		seeds = append(seeds, domain.DagNodeSeed{
			NodeID: node.ID,
			Record: domain.SessionRecord{
				ProjectID:       projectID,
				Kind:            domain.KindWorker,
				Harness:         domain.HarnessClaudeCode,
				DisplayName:     node.Name,
				SchedulingState: domain.SchedulingImmediate,
				CreatedAt:       now,
				UpdatedAt:       now,
			},
			Dependencies: dependencySeeds(node.DependsOn),
		})
	}
	return seeds, nil
}

func dependencySeeds(ids []domain.DagNodeID) []domain.DagNodeDependencySeed {
	out := make([]domain.DagNodeDependencySeed, 0, len(ids))
	for _, id := range ids {
		out = append(out, domain.DagNodeDependencySeed{NodeID: id, RequiredState: domain.DependencyMerged})
	}
	return out
}

type fakeSnapshots struct {
	byID map[domain.SessionID]domain.DependencySnapshot
}

func (f fakeSnapshots) DependencySnapshots(context.Context, []domain.SessionID) (map[domain.SessionID]domain.DependencySnapshot, error) {
	if f.byID == nil {
		return map[domain.SessionID]domain.DependencySnapshot{}, nil
	}
	return f.byID, nil
}

type fakeWaker struct{ projects []domain.ProjectID }

func (w *fakeWaker) Notify(projectID domain.ProjectID) { w.projects = append(w.projects, projectID) }

type fakeTerminator struct{ killed []domain.SessionID }

func (k *fakeTerminator) Kill(_ context.Context, id domain.SessionID) (bool, error) {
	k.killed = append(k.killed, id)
	return true, nil
}

func testService(store *fakeStore, planner fakePlanner, snapshots fakeSnapshots, waker *fakeWaker, term *fakeTerminator) *Service {
	return New(Deps{
		Store: store, Snapshots: snapshots, Planner: planner, Waker: waker, Sessions: term,
		Clock: func() time.Time { return time.Unix(2000, 0).UTC() },
		NewID: func() domain.DagRunID { return "dag-1" },
	})
}

func testProject() domain.ProjectRecord {
	return domain.ProjectRecord{
		ID: "p",
		Config: domain.ProjectConfig{
			Scheduling: domain.SchedulingConfig{MaxActiveSessions: 1},
		},
	}
}

func validNode(id domain.DagNodeID) domain.DagNodeSpec {
	return domain.DagNodeSpec{ID: id, Prompt: "do the work"}
}

func TestSubmitRejectsInvalidGraphsBeforePersisting(t *testing.T) {
	byID := func(id domain.DagNodeID, deps ...domain.DagNodeID) domain.DagNodeSpec {
		node := validNode(id)
		node.DependsOn = deps
		return node
	}
	for _, tc := range []struct {
		name   string
		config domain.SchedulingConfig
		spec   domain.DagRunSpec
		code   string
	}{
		{"missing name", domain.SchedulingConfig{}, domain.DagRunSpec{Nodes: []domain.DagNodeSpec{validNode("a")}}, "DAG_RUN_NAME_INVALID"},
		{"no nodes", domain.SchedulingConfig{}, domain.DagRunSpec{Name: "x"}, "DAG_RUN_EMPTY"},
		{"over limit", domain.SchedulingConfig{MaxDagNodesPerRun: 1}, domain.DagRunSpec{Name: "x", Nodes: []domain.DagNodeSpec{validNode("a"), validNode("b")}}, "DAG_RUN_LIMIT"},
		{"duplicate node", domain.SchedulingConfig{}, domain.DagRunSpec{Name: "x", Nodes: []domain.DagNodeSpec{validNode("a"), validNode("a")}}, "DAG_NODE_DUPLICATE"},
		{"duplicate dependency", domain.SchedulingConfig{}, domain.DagRunSpec{Name: "x", Nodes: []domain.DagNodeSpec{validNode("a"), byID("b", "a", "a")}}, "DAG_NODE_DUPLICATE"},
		{"self dependency", domain.SchedulingConfig{}, domain.DagRunSpec{Name: "x", Nodes: []domain.DagNodeSpec{byID("a", "a")}}, "DAG_NODE_SELF_DEPENDENCY"},
		{"unknown dependency", domain.SchedulingConfig{}, domain.DagRunSpec{Name: "x", Nodes: []domain.DagNodeSpec{byID("a", "ghost")}}, "DAG_NODE_DEPENDENCY_UNKNOWN"},
		{"cycle", domain.SchedulingConfig{}, domain.DagRunSpec{Name: "x", Nodes: []domain.DagNodeSpec{byID("a", "b"), byID("b", "a")}}, "DAG_NODE_CYCLE"},
		{"missing prompt", domain.SchedulingConfig{}, domain.DagRunSpec{Name: "x", Nodes: []domain.DagNodeSpec{{ID: "a"}}}, "DAG_NODE_PROMPT_INVALID"},
		{"empty node id", domain.SchedulingConfig{}, domain.DagRunSpec{Name: "x", Nodes: []domain.DagNodeSpec{{ID: " ", Prompt: "p"}}}, "DAG_NODE_ID_INVALID"},
		{"unknown required state", domain.SchedulingConfig{}, domain.DagRunSpec{Name: "x", Nodes: []domain.DagNodeSpec{{ID: "a", Prompt: "p", RequiredState: "sometimes"}}}, "DAG_NODE_STATE_INVALID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := testProject()
			project.Config.Scheduling = tc.config
			store := newFakeStore(project)
			svc := testService(store, fakePlanner{}, fakeSnapshots{}, &fakeWaker{}, &fakeTerminator{})

			_, err := svc.Submit(context.Background(), "p", tc.spec)
			var apiErr *apierr.Error
			if !errors.As(err, &apiErr) || apiErr.Code != tc.code {
				t.Fatalf("err = %v, want code %s", err, tc.code)
			}
			if store.createCalls != 0 {
				t.Fatalf("invalid graph reached the store %d times", store.createCalls)
			}
		})
	}
}

func TestSubmitCreatesNodesNonAdmitted(t *testing.T) {
	store := newFakeStore(testProject())
	waker := &fakeWaker{}
	svc := testService(store, fakePlanner{}, fakeSnapshots{}, waker, &fakeTerminator{})

	view, err := svc.Submit(context.Background(), "p", domain.DagRunSpec{
		Name: "feature-x",
		Nodes: []domain.DagNodeSpec{
			validNode("a"),
			validNode("b"),
			{ID: "c", Prompt: "after a", DependsOn: []domain.DagNodeID{"a"}},
		},
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if store.createCalls != 1 {
		t.Fatalf("store calls = %d", store.createCalls)
	}
	if len(waker.projects) != 1 || waker.projects[0] != "p" {
		t.Fatalf("waker = %+v", waker.projects)
	}
	if view.Status != domain.DagRunPending {
		t.Fatalf("run status = %s, want pending", view.Status)
	}
	nodes := map[domain.DagNodeID]domain.DagNodeView{}
	for _, node := range view.Nodes {
		nodes[node.NodeID] = node
	}
	// MaxActiveSessions=1: the first root takes the only slot, the second root
	// is blocked by the project limit, and the dependent waits on its edge.
	if nodes["a"].Status != domain.DagNodeQueued {
		t.Fatalf("a = %s, want queued", nodes["a"].Status)
	}
	if nodes["b"].Status != domain.DagNodeBlocked || nodes["b"].BlockedReason != string(domain.BlockedProjectLimit) {
		t.Fatalf("b = %s/%s, want blocked/project_limit", nodes["b"].Status, nodes["b"].BlockedReason)
	}
	if nodes["c"].Status != domain.DagNodeBlocked || nodes["c"].BlockedReason != string(domain.BlockedDependencyPending) {
		t.Fatalf("c = %s/%s, want blocked/dependency_pending", nodes["c"].Status, nodes["c"].BlockedReason)
	}
	if len(nodes["c"].DependsOn) != 1 || nodes["c"].DependsOn[0] != "a" {
		t.Fatalf("c depends on %+v", nodes["c"].DependsOn)
	}
	if view.Counts.Total != 3 || view.Counts.Queued != 1 || view.Counts.Blocked != 2 {
		t.Fatalf("counts = %+v", view.Counts)
	}
}

func TestSubmitUnlimitedProjectQueuesEveryRoot(t *testing.T) {
	project := testProject()
	project.Config.Scheduling = domain.SchedulingConfig{}
	store := newFakeStore(project)
	svc := testService(store, fakePlanner{}, fakeSnapshots{}, &fakeWaker{}, nil)

	view, err := svc.Submit(context.Background(), "p", domain.DagRunSpec{
		Name:  "roots",
		Nodes: []domain.DagNodeSpec{validNode("a"), validNode("b")},
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	for _, node := range view.Nodes {
		if node.Status != domain.DagNodeQueued {
			t.Fatalf("node %s = %s, want queued", node.NodeID, node.Status)
		}
	}
}

func TestGetDerivesAndPersistsMovedStatus(t *testing.T) {
	store := newFakeStore(testProject())
	svc := testService(store, fakePlanner{}, fakeSnapshots{}, &fakeWaker{}, nil)
	now := time.Unix(1000, 0).UTC()
	runID := domain.DagRunID("dag-1")
	nodeID := domain.DagNodeID("a")
	store.runs[runID] = domain.DagRun{ID: runID, ProjectID: "p", Name: "x", Status: domain.DagRunPending, CreatedAt: now}
	store.nodes[runID] = []domain.SessionRecord{{
		ID: "node-1", ProjectID: "p", DagRunID: &runID, DagNodeID: &nodeID,
		SchedulingState: domain.SchedulingAdmitted, CreatedAt: now,
	}}

	view, err := svc.Get(context.Background(), runID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if view.Status != domain.DagRunRunning {
		t.Fatalf("status = %s, want running", view.Status)
	}
	if len(store.statusUpdates) != 1 || store.statusUpdates[0].status != domain.DagRunRunning {
		t.Fatalf("status updates = %+v", store.statusUpdates)
	}

	// A terminated node without a success fact fails the run while no node is
	// admitted, and the moved status is persisted once.
	store.runs[runID] = domain.DagRun{ID: runID, ProjectID: "p", Name: "x", Status: domain.DagRunPending, CreatedAt: now}
	store.nodes[runID][0].IsTerminated = true
	store.statusUpdates = nil
	view, err = svc.Get(context.Background(), runID)
	if err != nil {
		t.Fatalf("get failed node: %v", err)
	}
	if view.Status != domain.DagRunFailed {
		t.Fatalf("status = %s, want failed", view.Status)
	}
	if len(store.statusUpdates) != 1 || store.statusUpdates[0].status != domain.DagRunFailed {
		t.Fatalf("status updates = %+v", store.statusUpdates)
	}

	// A terminal run never moves again, so its stored status is not rewritten.
	store.runs[runID] = domain.DagRun{ID: runID, ProjectID: "p", Name: "x", Status: domain.DagRunFailed, CreatedAt: now}
	store.statusUpdates = nil
	if _, err := svc.Get(context.Background(), runID); err != nil {
		t.Fatalf("get terminal run: %v", err)
	}
	if len(store.statusUpdates) != 0 {
		t.Fatalf("terminal run was rewritten: %+v", store.statusUpdates)
	}
}

func TestCancelCancelsPreLaunchNodesAndKeepsActiveWork(t *testing.T) {
	store := newFakeStore(testProject())
	term := &fakeTerminator{}
	waker := &fakeWaker{}
	svc := testService(store, fakePlanner{}, fakeSnapshots{}, waker, term)
	now := time.Unix(1000, 0).UTC()
	runID := domain.DagRunID("dag-1")
	queuedID, activeID := domain.DagNodeID("queued"), domain.DagNodeID("active")
	store.runs[runID] = domain.DagRun{ID: runID, ProjectID: "p", Name: "x", Status: domain.DagRunPending, CreatedAt: now}
	store.nodes[runID] = []domain.SessionRecord{
		{ID: "node-1", ProjectID: "p", DagRunID: &runID, DagNodeID: &queuedID, SchedulingState: domain.SchedulingQueued, CreatedAt: now},
		{ID: "node-2", ProjectID: "p", DagRunID: &runID, DagNodeID: &activeID, SchedulingState: domain.SchedulingAdmitted, CreatedAt: now},
	}

	view, err := svc.Cancel(context.Background(), runID, false)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if view.Status != domain.DagRunCancelled {
		t.Fatalf("status = %s, want cancelled", view.Status)
	}
	if store.cancelledNode != 1 {
		t.Fatalf("cancelled nodes = %d, want 1", store.cancelledNode)
	}
	if len(term.killed) != 0 {
		t.Fatalf("active work was killed without includeActive: %v", term.killed)
	}
	if len(waker.projects) != 1 {
		t.Fatalf("waker = %+v", waker.projects)
	}

	// includeActive kills the admitted node, but never a pre-launch one: those
	// are cancelled by the store, not by the runtime.
	store.runs[runID] = domain.DagRun{ID: runID, ProjectID: "p", Name: "x", Status: domain.DagRunPending, CreatedAt: now}
	store.nodes[runID] = []domain.SessionRecord{
		{ID: "node-1", ProjectID: "p", DagRunID: &runID, DagNodeID: &queuedID, SchedulingState: domain.SchedulingQueued, CreatedAt: now},
		{ID: "node-2", ProjectID: "p", DagRunID: &runID, DagNodeID: &activeID, SchedulingState: domain.SchedulingAdmitted, CreatedAt: now},
	}
	if _, err := svc.Cancel(context.Background(), runID, true); err != nil {
		t.Fatalf("cancel include active: %v", err)
	}
	if len(term.killed) != 1 || term.killed[0] != "node-2" {
		t.Fatalf("killed = %v, want [node-2]", term.killed)
	}
}

func TestCancelRejectsFinishedRun(t *testing.T) {
	store := newFakeStore(testProject())
	svc := testService(store, fakePlanner{}, fakeSnapshots{}, &fakeWaker{}, nil)
	store.runs["dag-1"] = domain.DagRun{ID: "dag-1", ProjectID: "p", Name: "x", Status: domain.DagRunCompleted}

	_, err := svc.Cancel(context.Background(), "dag-1", false)
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "DAG_RUN_NOT_CANCELLABLE" {
		t.Fatalf("err = %v, want DAG_RUN_NOT_CANCELLABLE", err)
	}
	if len(store.cancelledRuns) != 0 {
		t.Fatal("finished run reached the store cancel")
	}
}

func TestSubmitMapsPlannerHarnessError(t *testing.T) {
	store := newFakeStore(testProject())
	svc := testService(store, fakePlanner{err: errors.New("unknown agent harness \"nope\"")}, fakeSnapshots{}, nil, nil)

	_, err := svc.Submit(context.Background(), "p", domain.DagRunSpec{Name: "x", Nodes: []domain.DagNodeSpec{validNode("a")}})
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "DAG_NODE_HARNESS_INVALID" {
		t.Fatalf("err = %v, want DAG_NODE_HARNESS_INVALID", err)
	}
	if store.createCalls != 0 {
		t.Fatalf("store calls = %d", store.createCalls)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return string(digits[i:])
}
