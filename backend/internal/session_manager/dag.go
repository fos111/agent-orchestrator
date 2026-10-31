package sessionmanager

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// DagNodeSeeds builds the persisted node records for a DAG run without writing
// anything. The DAG service validates the graph, then persists every returned
// seed and its edges in one transaction.
//
// A node is a parentless worker session. It carries the same queued spawn
// request an ordinary dependency spawn persists, so the scheduler launches it
// through the ordinary launch path and re-resolves every environment-dependent
// check at that point.
func (m *Manager) DagNodeSeeds(ctx context.Context, projectID domain.ProjectID, nodes []domain.DagNodeSpec) ([]domain.DagNodeSeed, error) {
	if projectID == "" {
		return nil, fmt.Errorf("dag: a project id is required")
	}
	project, err := m.loadProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if project.Kind.WithDefault() == domain.ProjectKindScratch {
		return nil, fmt.Errorf("dag: scratch projects do not support DAG runs")
	}
	now := m.clock()
	seeds := make([]domain.DagNodeSeed, 0, len(nodes))
	for _, node := range nodes {
		cfg := ports.SpawnConfig{
			ProjectID:       projectID,
			Kind:            domain.KindWorker,
			Harness:         node.Harness,
			Prompt:          node.Prompt,
			DisplayName:     dagNodeDisplayName(node),
			RequestedOutput: node.RequestedOutput,
		}
		cfg.Harness = effectiveHarness(cfg.Harness, cfg.Kind, project.Config)
		if cfg.Harness == "" {
			return nil, fmt.Errorf("dag: %w: configure project %s.agent or pass a node harness", ErrMissingHarness, roleConfigName(cfg.Kind))
		}
		if _, ok := m.agents.Agent(cfg.Harness); !ok {
			return nil, fmt.Errorf("dag: %w: %q", ErrUnknownHarness, cfg.Harness)
		}
		if cfg.RequestedOutput == domain.RequestedOutputArtifact && m.artifactDir("") == "" {
			return nil, fmt.Errorf("dag: %w", ErrArtifactOutputUnavailable)
		}
		seed := seedRecord(cfg, project.Config, now)
		// The persisted request deliberately keeps RequestedMode empty: the
		// daemon preference may have changed while the node waited, and launch
		// re-resolves it exactly as an ordinary queued spawn does.
		raw, err := encodeQueuedSpawnConfig(cfg)
		if err != nil {
			return nil, err
		}
		seed.QueuedSpawnConfig = raw
		seed.ProvisionState = domain.SessionProvisionProvisioning
		seed.SchedulingState = domain.SchedulingQueued
		dependencies := make([]domain.DagNodeDependencySeed, 0, len(node.DependsOn))
		for _, dep := range node.DependsOn {
			dependencies = append(dependencies, domain.DagNodeDependencySeed{
				NodeID:        dep,
				RequiredState: dependencyRequiredState(node, dep),
			})
		}
		seeds = append(seeds, domain.DagNodeSeed{
			NodeID:       node.ID,
			Record:       seed,
			Dependencies: dependencies,
		})
	}
	return seeds, nil
}

// dagNodeDisplayName applies the submission's display-name rule: an empty node
// name reads as the node id.
func dagNodeDisplayName(node domain.DagNodeSpec) string {
	if node.Name != "" {
		return node.Name
	}
	return string(node.ID)
}

// dependencyRequiredState maps the DAG-local required-state vocabulary onto the
// scheduler's edge vocabulary. The DAG service has already validated it.
func dependencyRequiredState(node domain.DagNodeSpec, _ domain.DagNodeID) domain.DependencyRequiredState {
	state := node.RequiredState
	if state == "" {
		state = domain.DefaultDagRequiredState(node.RequestedOutput)
	}
	return domain.DependencyRequiredState(state)
}
