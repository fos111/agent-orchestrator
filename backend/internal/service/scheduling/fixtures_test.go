package scheduling_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// These fixtures are shared inputs for scheduling load and crash-recovery work.
// The test keeps their documented shape and counts from drifting.

type spawnFixture struct {
	ProjectID     string                `json:"projectId"`
	ProjectConfig domain.ProjectConfig  `json:"projectConfig"`
	Sessions      []spawnFixtureSession `json:"sessions"`
}

type spawnFixtureSession struct {
	ID              string                   `json:"id"`
	DisplayName     string                   `json:"displayName"`
	Prompt          string                   `json:"prompt"`
	RequestedOutput domain.RequestedOutput   `json:"requestedOutput,omitempty"`
	DependsOn       []spawnFixtureDependency `json:"dependsOn,omitempty"`
}

type spawnFixtureDependency struct {
	SessionID     string                         `json:"sessionId"`
	RequiredState domain.DependencyRequiredState `json:"requiredState"`
}

type crashFixture struct {
	Cases []crashCase `json:"cases"`
}

type crashCase struct {
	ID       string        `json:"id"`
	Phase    string        `json:"phase"`
	Before   crashBefore   `json:"before"`
	Recovery string        `json:"recovery"`
	Expected crashExpected `json:"expected"`
	Why      string        `json:"why"`
}

type crashBefore struct {
	SchedulingState string `json:"schedulingState"`
	BlockedReason   string `json:"blockedReason"`
	ProvisionState  string `json:"provisionState"`
	HasWorkspace    bool   `json:"hasWorkspace"`
	HasRuntime      bool   `json:"hasRuntime"`
	IsTerminated    bool   `json:"isTerminated"`
	DependencyFact  string `json:"dependencyFact"`
	ProjectFull     bool   `json:"projectFull"`
	TeamFull        bool   `json:"teamFull"`
	LaunchResult    string `json:"launchResult"`
}

type crashExpected struct {
	SchedulingState   string `json:"schedulingState"`
	BlockedReason     string `json:"blockedReason"`
	ProvisionState    string `json:"provisionState"`
	Launched          bool   `json:"launched"`
	LaunchAttempted   bool   `json:"launchAttempted"`
	IsTerminated      bool   `json:"isTerminated"`
	AdmissionOverride bool   `json:"admissionOverride"`
}

func TestSchedulingFixtures(t *testing.T) {
	t.Run("project load fixture", testProjectFixture)
	t.Run("DAG load fixture", testDagFixture)
	t.Run("crash matrix", testCrashFixture)
}

func testProjectFixture(t *testing.T) {
	var fixture spawnFixture
	decodeStrictFixture(t, filepath.Join("testdata", "load", "project_100_sessions_200_edges.json"), &fixture)

	if fixture.ProjectID == "" {
		t.Fatal("projectId is empty")
	}
	if got := len(fixture.Sessions); got != 100 {
		t.Fatalf("sessions = %d, want 100", got)
	}

	scheduling := fixture.ProjectConfig.Scheduling
	if scheduling.MaxActiveSessions != 20 || scheduling.TeamLimit() != 4 ||
		scheduling.DependencyLimit() != 16 || scheduling.DagNodeLimit() != domain.MaxDagNodesPerRun {
		t.Fatalf("scheduling config = %+v, want the documented load limits", scheduling)
	}
	if scheduling.DefaultDependencyState != "auto" {
		t.Fatalf("defaultDependencyState = %q, want auto", scheduling.DefaultDependencyState)
	}

	position := make(map[string]int, len(fixture.Sessions))
	edges := 0
	states := make(map[domain.DependencyRequiredState]bool)
	artifactSessions := 0
	for index, session := range fixture.Sessions {
		if session.ID == "" || session.DisplayName == "" || session.Prompt == "" {
			t.Fatalf("session %d has an empty id, displayName, or prompt", index)
		}
		if _, exists := position[session.ID]; exists {
			t.Fatalf("duplicate session id %q", session.ID)
		}
		if session.RequestedOutput != "" && session.RequestedOutput != domain.RequestedOutputArtifact {
			t.Fatalf("session %q requestedOutput = %q, want empty or artifact", session.ID, session.RequestedOutput)
		}
		if session.RequestedOutput == domain.RequestedOutputArtifact {
			artifactSessions++
		}
		if got := len(session.DependsOn); got > scheduling.DependencyLimit() {
			t.Fatalf("session %q has %d dependencies, limit is %d", session.ID, got, scheduling.DependencyLimit())
		}
		seen := make(map[string]bool, len(session.DependsOn))
		for _, edge := range session.DependsOn {
			if !edge.RequiredState.Valid() {
				t.Fatalf("session %q has requiredState %q", session.ID, edge.RequiredState)
			}
			if seen[edge.SessionID] {
				t.Fatalf("session %q repeats dependency %q", session.ID, edge.SessionID)
			}
			seen[edge.SessionID] = true
			if _, earlier := position[edge.SessionID]; !earlier {
				t.Fatalf("session %q depends on %q, which is not an earlier session", session.ID, edge.SessionID)
			}
			states[edge.RequiredState] = true
			edges++
		}
		position[session.ID] = index
	}

	if edges != 200 {
		t.Fatalf("edges = %d, want 200", edges)
	}
	if artifactSessions == 0 {
		t.Fatal("fixture has no artifact-requested session")
	}
	for _, state := range []domain.DependencyRequiredState{
		domain.DependencyReady, domain.DependencyMerged, domain.DependencyCompleted,
	} {
		if !states[state] {
			t.Fatalf("fixture never uses requiredState %q", state)
		}
	}
}

func testDagFixture(t *testing.T) {
	var fixture domain.DagRunSpec
	decodeStrictFixture(t, filepath.Join("testdata", "load", "dag_64_nodes.json"), &fixture)

	if strings.TrimSpace(fixture.Name) == "" {
		t.Fatal("DAG run name is empty")
	}
	if got := len(fixture.Nodes); got != domain.MaxDagNodesPerRun {
		t.Fatalf("nodes = %d, want %d", got, domain.MaxDagNodesPerRun)
	}

	position := make(map[domain.DagNodeID]int, len(fixture.Nodes))
	states := make(map[domain.DagRequiredState]bool)
	artifactNodes := 0
	for index, node := range fixture.Nodes {
		if node.ID == "" || node.Name == "" || node.Prompt == "" {
			t.Fatalf("node %d has an empty id, name, or prompt", index)
		}
		if _, exists := position[node.ID]; exists {
			t.Fatalf("duplicate node id %q", node.ID)
		}
		if node.RequestedOutput != "" && node.RequestedOutput != domain.RequestedOutputArtifact {
			t.Fatalf("node %q requestedOutput = %q, want empty or artifact", node.ID, node.RequestedOutput)
		}
		if node.RequestedOutput == domain.RequestedOutputArtifact {
			artifactNodes++
		}
		if node.RequiredState != "" && !node.RequiredState.Valid() {
			t.Fatalf("node %q has requiredState %q", node.ID, node.RequiredState)
		}
		if len(node.DependsOn) > 0 && node.RequiredState == "" {
			t.Fatalf("node %q has dependencies but no requiredState", node.ID)
		}
		seen := make(map[domain.DagNodeID]bool, len(node.DependsOn))
		for _, dependency := range node.DependsOn {
			if seen[dependency] {
				t.Fatalf("node %q repeats dependency %q", node.ID, dependency)
			}
			seen[dependency] = true
			if _, earlier := position[dependency]; !earlier {
				t.Fatalf("node %q depends on %q, which is not an earlier node", node.ID, dependency)
			}
			states[node.RequiredState] = true
		}
		position[node.ID] = index
	}

	if artifactNodes == 0 {
		t.Fatal("fixture has no artifact node")
	}
	for _, state := range []domain.DagRequiredState{
		domain.DagRequiredReady, domain.DagRequiredMerged, domain.DagRequiredCompleted,
	} {
		if !states[state] {
			t.Fatalf("fixture never uses requiredState %q", state)
		}
	}
}

func testCrashFixture(t *testing.T) {
	var fixture crashFixture
	decodeStrictFixture(t, filepath.Join("testdata", "crash", "matrix.json"), &fixture)

	if len(fixture.Cases) == 0 {
		t.Fatal("crash matrix has no cases")
	}

	schedulingStates := stringSet("immediate", "queued", "blocked", "admitted", "cancelled")
	blockedReasons := stringSet(
		"", "dependency_pending", "dependency_failed", "dependency_cancelled",
		"dependency_missing", "dependency_cycle", "dependency_unsatisfiable",
		"project_limit", "team_limit", "launch_failed", "invalid",
	)
	provisionStates := stringSet("queued", "provisioning", "ready", "failed")
	dependencyFacts := stringSet(
		"none", "pending", "satisfied", "failed", "cancelled", "missing",
		"unsatisfiable", "invalid",
	)
	launchResults := stringSet("not_attempted", "succeeded", "failed")
	phases := stringSet("reserve", "evaluate", "admit", "launch", "recover", "cancel")
	recoveries := stringSet("boot", "sweep", "admit", "force_admit", "none")

	seen := make(map[string]bool, len(fixture.Cases))
	for _, testCase := range fixture.Cases {
		if testCase.ID == "" || testCase.Why == "" {
			t.Fatalf("case %q has an empty id or why", testCase.ID)
		}
		if seen[testCase.ID] {
			t.Fatalf("duplicate case id %q", testCase.ID)
		}
		seen[testCase.ID] = true

		if !phases[testCase.Phase] {
			t.Fatalf("case %q phase = %q", testCase.ID, testCase.Phase)
		}
		if !recoveries[testCase.Recovery] {
			t.Fatalf("case %q recovery = %q", testCase.ID, testCase.Recovery)
		}
		before, expected := testCase.Before, testCase.Expected
		if !schedulingStates[before.SchedulingState] {
			t.Fatalf("case %q before.schedulingState = %q", testCase.ID, before.SchedulingState)
		}
		if !schedulingStates[expected.SchedulingState] {
			t.Fatalf("case %q expected.schedulingState = %q", testCase.ID, expected.SchedulingState)
		}
		if !blockedReasons[before.BlockedReason] || !blockedReasons[expected.BlockedReason] {
			t.Fatalf("case %q has an unknown blockedReason", testCase.ID)
		}
		if !provisionStates[before.ProvisionState] || !provisionStates[expected.ProvisionState] {
			t.Fatalf("case %q has an unknown provisionState", testCase.ID)
		}
		if !dependencyFacts[before.DependencyFact] {
			t.Fatalf("case %q dependencyFact = %q", testCase.ID, before.DependencyFact)
		}
		if !launchResults[before.LaunchResult] {
			t.Fatalf("case %q launchResult = %q", testCase.ID, before.LaunchResult)
		}

		if expected.Launched && !expected.LaunchAttempted {
			t.Fatalf("case %q launched without a launch attempt", testCase.ID)
		}
		if expected.Launched && expected.ProvisionState != "ready" {
			t.Fatalf("case %q launched with provisionState %q", testCase.ID, expected.ProvisionState)
		}
		if expected.BlockedReason == "launch_failed" && expected.ProvisionState != "failed" {
			t.Fatalf("case %q is blocked(launch_failed) with provisionState %q", testCase.ID, expected.ProvisionState)
		}
		if expected.SchedulingState == "cancelled" && !expected.IsTerminated {
			t.Fatalf("case %q is cancelled but not terminated", testCase.ID)
		}
		if expected.IsTerminated != before.IsTerminated {
			t.Fatalf("case %q changes isTerminated without an explicit cancel", testCase.ID)
		}
		if expected.AdmissionOverride != (testCase.Recovery == "force_admit") {
			t.Fatalf("case %q admissionOverride = %v with recovery %q", testCase.ID, expected.AdmissionOverride, testCase.Recovery)
		}
	}
}

func decodeStrictFixture(t *testing.T, path string, target any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		t.Fatalf("decode %s: trailing data", path)
	}
}

func stringSet(values ...string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}
