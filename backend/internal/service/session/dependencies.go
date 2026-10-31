package session

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// dependencyGraphReader is the narrow optional read of the stored dependency
// graph. The session Service only needs it to reject a create against a graph
// that is already cyclic; a store without the capability is not an error
// because no cycle can be introduced by a brand-new row.
type dependencyGraphReader interface {
	ListSessionDependencies(ctx context.Context, id domain.SessionID) ([]domain.SessionDependency, error)
}

// resolveSpawnDependencies validates the requested edges and fills each edge's
// required state. It is the create-time gate; the scheduler re-evaluates every
// edge when facts change, and the empty state defaults from the dependency's
// output type.
func (s *Service) resolveSpawnDependencies(ctx context.Context, cfg ports.SpawnConfig, project domain.ProjectRecord) ([]domain.SessionDependencySpec, error) {
	specs := cfg.Dependencies
	if len(specs) == 0 {
		return nil, nil
	}
	if cfg.ProjectID == "" {
		return nil, apierr.Invalid("DEPENDENCY_STANDALONE_UNSUPPORTED",
			"Dependencies require a registered project; a standalone session cannot depend on another session", nil)
	}
	if cfg.Kind != "" && cfg.Kind != domain.KindWorker {
		return nil, apierr.Invalid("DEPENDENCY_STANDALONE_UNSUPPORTED",
			"Orchestrator sessions cannot depend on another session", nil)
	}
	if limit := project.Config.Scheduling.DependencyLimit(); len(specs) > limit {
		return nil, apierr.Invalid("DEPENDENCY_LIMIT",
			fmt.Sprintf("At most %d dependencies are allowed per session", limit), nil)
	}

	seen := make(map[domain.SessionID]bool, len(specs))
	out := make([]domain.SessionDependencySpec, 0, len(specs))
	for _, spec := range specs {
		id := spec.SessionID
		if id == "" {
			return nil, apierr.Invalid("DEPENDENCY_NOT_FOUND", "A dependency session id is required", nil)
		}
		if cfg.ResumeSessionID != "" && id == cfg.ResumeSessionID {
			return nil, apierr.Invalid("DEPENDENCY_SELF", "A session cannot depend on itself", nil)
		}
		if seen[id] {
			return nil, apierr.Invalid("DEPENDENCY_DUPLICATE",
				fmt.Sprintf("Dependency %s was requested more than once", id), nil)
		}
		seen[id] = true

		dep, found, err := s.store.GetSession(ctx, id)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, apierr.Invalid("DEPENDENCY_NOT_FOUND",
				fmt.Sprintf("Dependency session %s does not exist", id), nil)
		}
		if dep.ProjectID != cfg.ProjectID {
			return nil, apierr.Invalid("DEPENDENCY_CROSS_PROJECT",
				fmt.Sprintf("Dependency %s belongs to another project", id), nil)
		}
		if dep.Kind != "" && dep.Kind != domain.KindWorker {
			return nil, apierr.Invalid("DEPENDENCY_KIND_INVALID",
				fmt.Sprintf("Dependency %s is not a worker session", id), nil)
		}
		required, err := s.resolveDependencyRequiredState(ctx, project.Config.Scheduling, dep, spec.RequiredState)
		if err != nil {
			return nil, err
		}
		out = append(out, domain.SessionDependencySpec{SessionID: id, RequiredState: required})
	}
	if err := s.rejectCyclicDependencyGraph(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// resolveDependencyRequiredState applies the project default, then the
// output-type rule, then rejects a state the dependency can never reach.
func (s *Service) resolveDependencyRequiredState(
	ctx context.Context,
	scheduling domain.SchedulingConfig,
	dep domain.SessionRecord,
	requested domain.DependencyRequiredState,
) (domain.DependencyRequiredState, error) {
	state := requested
	if state == "" {
		switch scheduling.DefaultDependencyState {
		case "", "auto":
			state = domain.DefaultDependencyRequiredState(dep.OutputType)
		default:
			state = domain.DependencyRequiredState(scheduling.DefaultDependencyState)
		}
	}
	if !state.Valid() {
		return "", apierr.Invalid("DEPENDENCY_STATE_INVALID",
			fmt.Sprintf("Dependency %s has an unknown required state %q", dep.ID, state), nil)
	}
	if state == domain.DependencyMerged && dep.IsTerminated && !dep.OutputType.HasPR() {
		// A terminated session can no longer open a PR. A live one still can,
		// even before its output type has been observed.
		prs, err := s.store.ListPRFactsForSession(ctx, dep.ID)
		if err != nil {
			return "", err
		}
		if len(prs) == 0 {
			return "", apierr.Invalid("DEPENDENCY_STATE_INVALID",
				fmt.Sprintf("Dependency %s ended without a pull request, so it cannot satisfy merged", dep.ID), nil)
		}
	}
	return state, nil
}

// rejectCyclicDependencyGraph rejects a create whose dependency set already
// closes a cycle. The new row has no outgoing edges yet, so it cannot itself
// close one; this guards a graph written by an older or buggy daemon, which the
// scheduler would otherwise block forever.
func (s *Service) rejectCyclicDependencyGraph(ctx context.Context, specs []domain.SessionDependencySpec) error {
	graph, ok := s.store.(dependencyGraphReader)
	if !ok {
		return nil
	}
	const maxWalk = 256
	const (
		unvisited = iota
		onStack
		done
	)
	state := make(map[domain.SessionID]int, len(specs))
	walked := 0
	var walk func(id domain.SessionID) error
	walk = func(id domain.SessionID) error {
		switch state[id] {
		case onStack:
			return apierr.Invalid("DEPENDENCY_CYCLE",
				fmt.Sprintf("The dependency graph around %s is cyclic", id), nil)
		case done:
			return nil
		}
		walked++
		if walked > maxWalk {
			return apierr.Invalid("DEPENDENCY_CYCLE", "The dependency graph is too deep to validate", nil)
		}
		state[id] = onStack
		edges, err := graph.ListSessionDependencies(ctx, id)
		if err != nil {
			return err
		}
		for _, edge := range edges {
			if err := walk(edge.DependsOnSessionID); err != nil {
				return err
			}
		}
		state[id] = done
		return nil
	}
	for _, spec := range specs {
		if err := walk(spec.SessionID); err != nil {
			return err
		}
	}
	return nil
}
