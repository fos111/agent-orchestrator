package sessionmanager

import (
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func queuedTestConfig(dependency domain.SessionID) ports.SpawnConfig {
	return ports.SpawnConfig{
		ProjectID: "mer",
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessClaudeCode,
		Prompt:    "Add integration tests for the new endpoint.",
		Branch:    "ao/test-api",
		Dependencies: []domain.SessionDependencySpec{{
			SessionID:     dependency,
			RequiredState: domain.DependencyMerged,
		}},
	}
}

func TestReserveSpawnPersistsQueuedRowWithoutWorkspace(t *testing.T) {
	m, st, rt, ws := newManager()
	rec, err := m.ReserveSpawn(ctx, queuedTestConfig("mer-0"))
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if rec.SchedulingState != domain.SchedulingBlocked || rec.BlockedReason != string(domain.BlockedDependencyPending) {
		t.Fatalf("reserved row = %+v, want blocked with dependency_pending", rec)
	}
	if rec.QueuedSpawnConfig == "" {
		t.Fatal("reserved row has no queued spawn config")
	}
	if rec.Metadata.WorkspacePath != "" || rec.Metadata.RuntimeHandleID != "" {
		t.Fatalf("reservation owns runtime facts: %+v", rec.Metadata)
	}
	if ws.createCount != 0 {
		t.Fatalf("workspace creates = %d, want 0 for a queued session", ws.createCount)
	}
	if rt.created != 0 {
		t.Fatalf("runtime creates = %d, want 0 for a queued session", rt.created)
	}
	edges := st.dependencies[rec.ID]
	if len(edges) != 1 || edges[0].DependsOnSessionID != "mer-0" || edges[0].RequiredState != domain.DependencyMerged {
		t.Fatalf("stored edges = %+v, want one merged edge to mer-0", edges)
	}
}

func TestLaunchSpawnMaterializesAdmittedReservationExactlyOnce(t *testing.T) {
	m, st, rt, ws := newManager()
	rec, err := m.ReserveSpawn(ctx, queuedTestConfig("mer-0"))
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	admitted := st.sessions[rec.ID]
	admitted.SchedulingState = domain.SchedulingAdmitted
	st.sessions[rec.ID] = admitted

	launched, err := m.LaunchSpawn(ctx, rec.ID)
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if launched.ID != rec.ID {
		t.Fatalf("launched id = %s, want %s", launched.ID, rec.ID)
	}
	if ws.createCount != 1 || rt.created != 1 {
		t.Fatalf("workspace creates = %d, runtime creates = %d, want exactly one each", ws.createCount, rt.created)
	}
	if ws.lastCfg.Branch != "ao/test-api" {
		t.Fatalf("workspace branch = %q, want the persisted branch", ws.lastCfg.Branch)
	}
	if _, err := m.LaunchSpawn(ctx, rec.ID); err == nil {
		t.Fatal("second launch of the same reservation was accepted")
	}
	if ws.createCount != 1 || rt.created != 1 {
		t.Fatalf("second launch created workspace=%d runtime=%d, want no second launch", ws.createCount, rt.created)
	}
}

func TestLaunchSpawnRefusesQueuedSession(t *testing.T) {
	m, _, rt, ws := newManager()
	rec, err := m.ReserveSpawn(ctx, queuedTestConfig("mer-0"))
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if _, err := m.LaunchSpawn(ctx, rec.ID); err == nil {
		t.Fatal("a queued session was launched without admission")
	}
	if ws.createCount != 0 || rt.created != 0 {
		t.Fatalf("workspace=%d runtime=%d, want none", ws.createCount, rt.created)
	}
}

func TestQueuedSpawnConfigRoundTrip(t *testing.T) {
	cfg := ports.SpawnConfig{
		ProjectID:       "mer",
		IssueID:         "42",
		ParentSessionID: "mer-1",
		TrackerProvider: domain.TrackerProviderGitHub,
		IssueContext:    "Issue body",
		Kind:            domain.KindWorker,
		Harness:         domain.HarnessCodex,
		Branch:          "ao/x",
		Prompt:          "do the thing",
		DisplayName:     "thing",
		AgentAccountID:  "account-1",
		AgentConfig: domain.AgentConfig{
			Model: "gpt-5", Effort: "high", Mode: "auto",
			Permissions: domain.PermissionModeAcceptEdits,
			Launch:      domain.AgentLaunchOptions{FastMode: true, AutoCompactWindow: "1m"},
		},
		EffortOverride:      true,
		AgentConfigResolved: true,
		RequestedMode:       domain.SessionModeChat,
		RequestedOutput:     domain.RequestedOutputArtifact,
		Async:               true,
		NameFromPrompt:      true,
		KeepDisplayName:     true,
	}
	raw, err := encodeQueuedSpawnConfig(cfg)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := decodeQueuedSpawnConfig(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ProjectID != cfg.ProjectID || got.IssueID != cfg.IssueID || got.ParentSessionID != cfg.ParentSessionID ||
		got.Kind != cfg.Kind || got.Harness != cfg.Harness || got.Branch != cfg.Branch || got.Prompt != cfg.Prompt ||
		got.DisplayName != cfg.DisplayName || got.AgentAccountID != cfg.AgentAccountID ||
		got.RequestedMode != cfg.RequestedMode || got.RequestedOutput != cfg.RequestedOutput ||
		got.Async != cfg.Async || got.NameFromPrompt != cfg.NameFromPrompt || got.KeepDisplayName != cfg.KeepDisplayName {
		t.Fatalf("round trip = %+v, want %+v", got, cfg)
	}
	if got.AgentConfig != cfg.AgentConfig {
		t.Fatalf("agent config = %+v, want %+v", got.AgentConfig, cfg.AgentConfig)
	}
	if !got.EffortOverride || !got.AgentConfigResolved {
		t.Fatalf("flags = effort:%v resolved:%v, want both true", got.EffortOverride, got.AgentConfigResolved)
	}
}

func TestQueuedSpawnConfigRejectsAttachments(t *testing.T) {
	cfg := queuedTestConfig("mer-0")
	cfg.Attachments = []ports.SpawnAttachment{{Ext: ".png", Data: []byte("bytes")}}
	if _, err := encodeQueuedSpawnConfig(cfg); !errors.Is(err, ErrQueuedSpawnUnsupported) {
		t.Fatalf("err = %v, want ErrQueuedSpawnUnsupported", err)
	}
}

func TestDecodeQueuedSpawnConfigRejectsUnknownVersion(t *testing.T) {
	_, err := decodeQueuedSpawnConfig(`{"version":99,"prompt":"x"}`)
	if err == nil || !strings.Contains(err.Error(), "version 99") {
		t.Fatalf("err = %v, want an unsupported-version error", err)
	}
	if _, err := decodeQueuedSpawnConfig(""); err == nil {
		t.Fatal("empty config was accepted")
	}
}
