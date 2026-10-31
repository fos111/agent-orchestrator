package sessionmanager

import (
	"context"
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

// A queued session owns no workspace and no controller, so kill must be a
// single durable cancel: no workspace destroy, no runtime destroy, no shell
// teardown.
func TestKillQueuedSessionCancelsWithoutWorkspaceCleanup(t *testing.T) {
	m, st, rt, ws := newManager()
	rec, err := m.ReserveSpawn(ctx, queuedTestConfig("mer-0"))
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	freed, err := m.Kill(ctx, rec.ID)
	if err != nil {
		t.Fatalf("kill: %v", err)
	}
	if freed {
		t.Fatal("killing a queued session freed a workspace it never owned")
	}
	got := st.sessions[rec.ID]
	if !got.IsTerminated || got.SchedulingState != domain.SchedulingCancelled {
		t.Fatalf("killed row = %+v, want terminated and cancelled", got)
	}
	if ws.destroyed != 0 || rt.destroyed != 0 {
		t.Fatalf("workspace destroys = %d, runtime destroys = %d, want none", ws.destroyed, rt.destroyed)
	}
	if ws.createCount != 0 || rt.created != 0 {
		t.Fatalf("workspace creates = %d, runtime creates = %d, want none", ws.createCount, rt.created)
	}
}

// Killing an admitted row that already materialized is the ordinary teardown
// path; the pre-launch short-circuit must not swallow it.
func TestKillLaunchedSessionUsesWorkspaceTeardown(t *testing.T) {
	m, st, rt, ws := newManager()
	rec, err := m.ReserveSpawn(ctx, queuedTestConfig("mer-0"))
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	admitted := st.sessions[rec.ID]
	admitted.SchedulingState = domain.SchedulingAdmitted
	st.sessions[rec.ID] = admitted
	if _, err := m.LaunchSpawn(ctx, rec.ID); err != nil {
		t.Fatalf("launch: %v", err)
	}
	if _, err := m.Kill(ctx, rec.ID); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if ws.destroyed != 1 || rt.destroyed != 1 {
		t.Fatalf("workspace destroys = %d, runtime destroys = %d, want one each", ws.destroyed, rt.destroyed)
	}
}

// fakeLaunchReadiness records the launch-time readiness probe. It lets a test
// flip a harness from installed to gone while the session sits in the queue.
type fakeLaunchReadiness struct {
	snapshot domain.AgentReadinessSnapshot
	err      error
	calls    int
	agentID  string
	purpose  domain.AgentReadinessPurpose
}

func (f *fakeLaunchReadiness) EnsureAgentReadiness(_ context.Context, agentID string, purpose domain.AgentReadinessPurpose) (domain.AgentReadinessSnapshot, error) {
	f.calls++
	f.agentID = agentID
	f.purpose = purpose
	return f.snapshot, f.err
}

func (f *fakeLaunchReadiness) InvalidateAgentInstallation(string)   {}
func (f *fakeLaunchReadiness) InvalidateAgentAuthentication(string) {}
func (f *fakeLaunchReadiness) RecheckAgent(string)                  {}

// A queued session must not launch with a harness that disappeared while it
// waited: the reserve-time check is only fast feedback, launch is authoritative.
func TestLaunchSpawnRechecksAgentReadiness(t *testing.T) {
	m, st, rt, ws := newManager()
	readiness := &fakeLaunchReadiness{snapshot: domain.AgentReadinessSnapshot{
		Installation: domain.AgentInstallationObservation{State: domain.AgentInstallationInstalled},
	}}
	m.SetAgentReadiness(readiness)
	rec, err := m.ReserveSpawn(ctx, queuedTestConfig("mer-0"))
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	admitted := st.sessions[rec.ID]
	admitted.SchedulingState = domain.SchedulingAdmitted
	st.sessions[rec.ID] = admitted

	// The harness is uninstalled while the row is admitted but not launched.
	readiness.snapshot.Installation.State = domain.AgentInstallationNotInstalled
	if _, err := m.LaunchSpawn(ctx, rec.ID); !errors.Is(err, ports.ErrAgentBinaryNotFound) {
		t.Fatalf("launch error = %v, want ErrAgentBinaryNotFound", err)
	}
	if readiness.calls != 1 || readiness.agentID != string(domain.HarnessClaudeCode) || readiness.purpose != domain.AgentReadinessPurposeLaunch {
		t.Fatalf("readiness probe = calls:%d agent:%q purpose:%q, want one launch probe for claude-code", readiness.calls, readiness.agentID, readiness.purpose)
	}
	if ws.createCount != 0 || rt.created != 0 {
		t.Fatalf("workspace=%d runtime=%d, want none after a readiness failure", ws.createCount, rt.created)
	}
	if got := st.sessions[rec.ID]; got.SchedulingState != domain.SchedulingAdmitted {
		t.Fatalf("scheduling state = %s, want the admitted row left for the scheduler to fail", got.SchedulingState)
	}

	// Reinstalling the harness lets the same admitted row launch.
	readiness.snapshot.Installation.State = domain.AgentInstallationInstalled
	if _, err := m.LaunchSpawn(ctx, rec.ID); err != nil {
		t.Fatalf("launch after reinstall: %v", err)
	}
	if ws.createCount != 1 || rt.created != 1 {
		t.Fatalf("workspace=%d runtime=%d, want one launch after reinstall", ws.createCount, rt.created)
	}
}
