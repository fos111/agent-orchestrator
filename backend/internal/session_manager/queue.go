package sessionmanager

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ErrQueuedSpawnUnsupported reports a spawn feature a queued session cannot
// carry. Attachments are bytes the row would have to hold for an unbounded
// time, so a queued spawn rejects them instead of silently dropping them at
// launch.
var ErrQueuedSpawnUnsupported = errors.New("queued spawn unsupported")

// queuedSpawnConfigVersion is the on-disk schema of sessions.queued_spawn_config.
// Bump it when a field changes meaning; decode refuses unknown versions rather
// than launching with a partially understood request.
const queuedSpawnConfigVersion = 1

// queuedSpawnConfig is the normalized spawn request a reserved session persists
// until admission. It is deliberately explicit rather than json.Marshal of
// ports.SpawnConfig: AgentConfig.Launch has no JSON form there, and attachments,
// client-request keys, automation runs, and preparation tokens must never be
// resurrected by a later launch.
type queuedSpawnConfig struct {
	Version int `json:"version"`

	ProjectID       string `json:"projectId,omitempty"`
	IssueID         string `json:"issueId,omitempty"`
	ParentSessionID string `json:"parentSessionId,omitempty"`
	TrackerProvider string `json:"trackerProvider,omitempty"`
	IssueContext    string `json:"issueContext,omitempty"`
	Kind            string `json:"kind,omitempty"`
	Harness         string `json:"harness,omitempty"`
	Branch          string `json:"branch,omitempty"`
	Prompt          string `json:"prompt,omitempty"`
	DisplayName     string `json:"displayName,omitempty"`
	AgentAccountID  string `json:"agentAccountId,omitempty"`

	Model           string                    `json:"model,omitempty"`
	Effort          string                    `json:"effort,omitempty"`
	Mode            string                    `json:"mode,omitempty"`
	Permissions     string                    `json:"permissions,omitempty"`
	Launch          domain.AgentLaunchOptions `json:"launch,omitempty"`
	EffortOverride  bool                      `json:"effortOverride,omitempty"`
	ConfigResolved  bool                      `json:"configResolved,omitempty"`
	RequestedMode   string                    `json:"requestedMode,omitempty"`
	RequestedOutput string                    `json:"requestedOutput,omitempty"`
	Async           bool                      `json:"async,omitempty"`
	NameFromPrompt  bool                      `json:"nameFromPrompt,omitempty"`
	KeepDisplayName bool                      `json:"keepDisplayName,omitempty"`
}

// encodeQueuedSpawnConfig renders the launchable subset of cfg. Callers pass
// the request after validation, so a rejected spawn never persists a config.
func encodeQueuedSpawnConfig(cfg ports.SpawnConfig) (string, error) {
	if len(cfg.Attachments) > 0 {
		return "", fmt.Errorf("spawn: %w: attachments cannot be queued; spawn without dependencies or attach after admission", ErrQueuedSpawnUnsupported)
	}
	out := queuedSpawnConfig{
		Version:         queuedSpawnConfigVersion,
		ProjectID:       string(cfg.ProjectID),
		IssueID:         string(cfg.IssueID),
		ParentSessionID: string(cfg.ParentSessionID),
		TrackerProvider: string(cfg.TrackerProvider),
		IssueContext:    cfg.IssueContext,
		Kind:            string(cfg.Kind),
		Harness:         string(cfg.Harness),
		Branch:          cfg.Branch,
		Prompt:          cfg.Prompt,
		DisplayName:     cfg.DisplayName,
		AgentAccountID:  cfg.AgentAccountID,
		Model:           cfg.AgentConfig.Model,
		Effort:          cfg.AgentConfig.Effort,
		Mode:            cfg.AgentConfig.Mode,
		Permissions:     string(cfg.AgentConfig.Permissions),
		Launch:          cfg.AgentConfig.Launch,
		EffortOverride:  cfg.EffortOverride,
		ConfigResolved:  cfg.AgentConfigResolved,
		RequestedMode:   string(cfg.RequestedMode),
		RequestedOutput: string(cfg.RequestedOutput),
		Async:           cfg.Async,
		NameFromPrompt:  cfg.NameFromPrompt,
		KeepDisplayName: cfg.KeepDisplayName,
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("spawn: encode queued config: %w", err)
	}
	return string(raw), nil
}

// decodeQueuedSpawnConfig rebuilds the launch request. It is the launch-time
// source of truth: the caller then re-runs every environment-dependent check.
func decodeQueuedSpawnConfig(raw string) (ports.SpawnConfig, error) {
	if strings.TrimSpace(raw) == "" {
		return ports.SpawnConfig{}, fmt.Errorf("spawn: session has no queued spawn config")
	}
	var in queuedSpawnConfig
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		return ports.SpawnConfig{}, fmt.Errorf("spawn: decode queued config: %w", err)
	}
	if in.Version != queuedSpawnConfigVersion {
		return ports.SpawnConfig{}, fmt.Errorf("spawn: queued config version %d is not supported", in.Version)
	}
	return ports.SpawnConfig{
		ProjectID:           domain.ProjectID(in.ProjectID),
		IssueID:             domain.IssueID(in.IssueID),
		ParentSessionID:     domain.SessionID(in.ParentSessionID),
		TrackerProvider:     domain.TrackerProvider(in.TrackerProvider),
		IssueContext:        in.IssueContext,
		Kind:                domain.SessionKind(in.Kind),
		Harness:             domain.AgentHarness(in.Harness),
		Branch:              in.Branch,
		Prompt:              in.Prompt,
		DisplayName:         in.DisplayName,
		AgentAccountID:      in.AgentAccountID,
		AgentConfig:         domain.AgentConfig{Model: in.Model, Effort: in.Effort, Mode: in.Mode, Permissions: domain.PermissionMode(in.Permissions), Launch: in.Launch},
		AgentConfigResolved: in.ConfigResolved,
		EffortOverride:      in.EffortOverride,
		RequestedMode:       domain.SessionMode(in.RequestedMode),
		RequestedOutput:     domain.RequestedOutput(in.RequestedOutput),
		Async:               in.Async,
		NameFromPrompt:      in.NameFromPrompt,
		KeepDisplayName:     in.KeepDisplayName,
	}, nil
}
