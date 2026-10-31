package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func sampleTeamJSON() string {
	return `{"rootSessionId":"ao-100","projectId":"demo",` +
		`"orchestrator":{"sessionId":"ao-100","kind":"orchestrator","isTerminated":false,"createdAt":"2026-10-31T12:00:00Z"},` +
		`"members":[{"sessionId":"ao-100","kind":"orchestrator","isTerminated":false,"createdAt":"2026-10-31T12:00:00Z"},` +
		`{"sessionId":"ao-101","parentSessionId":"ao-100","kind":"worker","displayName":"tests","isTerminated":false,"createdAt":"2026-10-31T12:00:00Z"}],` +
		`"activeWorkers":1,"maxActiveWorkers":4}`
}

func sampleTeamResponse() string {
	return `{"team":` + sampleTeamJSON() + `}`
}

func TestTeamListUsesProjectContext(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("AO_PROJECT_ID", "demo")
	t.Setenv("AO_SESSION_ID", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/internal/telemetry/cli-invoked":
			w.WriteHeader(http.StatusAccepted)
		case "/api/v1/projects/demo":
			_, _ = io.WriteString(w, `{"project":{"id":"demo","kind":"single_repo"}}`)
		case "/api/v1/projects/demo/teams":
			_, _ = io.WriteString(w, `{"teams":[`+sampleTeamJSON()+`]}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	writeRunFileFor(t, cfg, server)

	out, stderr, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "team", "ls")
	if err != nil {
		t.Fatalf("list: %v stderr=%s", err, stderr)
	}
	if !strings.Contains(out, "ao-100") || !strings.Contains(out, "1/4") {
		t.Fatalf("output = %q", out)
	}
}

func TestTeamListJSONOutput(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("AO_PROJECT_ID", "demo")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/internal/telemetry/cli-invoked":
			w.WriteHeader(http.StatusAccepted)
		case "/api/v1/projects/demo":
			_, _ = io.WriteString(w, `{"project":{"id":"demo","kind":"single_repo"}}`)
		case "/api/v1/projects/demo/teams":
			_, _ = io.WriteString(w, `{"teams":[`+sampleTeamJSON()+`]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	writeRunFileFor(t, cfg, server)

	out, stderr, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "team", "ls", "--json")
	if err != nil {
		t.Fatalf("list: %v stderr=%s", err, stderr)
	}
	var res teamListDTO
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if len(res.Teams) != 1 || res.Teams[0].RootSessionID != "ao-100" || res.Teams[0].MaxActiveWorkers != 4 {
		t.Fatalf("teams = %+v", res.Teams)
	}
}

func TestTeamStatusShowsLeadMembersAndLimit(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/internal/telemetry/cli-invoked":
			w.WriteHeader(http.StatusAccepted)
		case "/api/v1/teams/ao-100":
			_, _ = io.WriteString(w, sampleTeamResponse())
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	writeRunFileFor(t, cfg, server)

	out, stderr, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "team", "status", "ao-100")
	if err != nil {
		t.Fatalf("status: %v stderr=%s", err, stderr)
	}
	if !strings.Contains(out, "Team ao-100 in project demo") || !strings.Contains(out, "Workers: 1/4 active") ||
		!strings.Contains(out, "ao-101") {
		t.Fatalf("output = %q", out)
	}
}

func TestTeamStatusKeepsDaemonErrorEnvelope(t *testing.T) {
	cfg := setConfigEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/internal/telemetry/cli-invoked":
			w.WriteHeader(http.StatusAccepted)
		case "/api/v1/teams/ao-999":
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":"not_found","code":"TEAM_NOT_FOUND","message":"Unknown team root","requestId":"request-1"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	writeRunFileFor(t, cfg, server)

	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "team", "status", "ao-999")
	if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), "TEAM_NOT_FOUND") || !strings.Contains(err.Error(), "request-1") {
		t.Fatalf("err=%v exit=%d", err, ExitCode(err))
	}
}

func TestTeamMessageSendsAttributionAndReportsDeliveries(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("AO_SESSION_ID", "ao-100")
	var body teamMessageRequestDTO
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/internal/telemetry/cli-invoked":
			w.WriteHeader(http.StatusAccepted)
		case "/api/v1/teams/ao-100/messages":
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode body: %v", err)
			}
			_, _ = io.WriteString(w, `{"ok":true,"message":{"id":"msg-1","projectId":"demo","teamRootSessionId":"ao-100","senderSessionId":"ao-100","body":"Rebase on main.","createdAt":"2026-10-31T12:00:00Z"},`+
				`"deliveries":[{"sessionId":"ao-101","status":"delivered"},{"sessionId":"ao-102","status":"failed","error":"runtime unavailable"}],`+
				`"delivered":1,"failed":1}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	writeRunFileFor(t, cfg, server)

	out, stderr, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "team", "message", "ao-100", "Rebase on main.")
	if err != nil {
		t.Fatalf("message: %v stderr=%s", err, stderr)
	}
	if body.SenderSessionID != "ao-100" || body.Message != "Rebase on main." {
		t.Fatalf("posted body = %+v", body)
	}
	if !strings.Contains(out, "Broadcast msg-1 to 2 members: 1 delivered, 1 failed") ||
		!strings.Contains(out, "ao-102 failed: runtime unavailable") {
		t.Fatalf("output = %q", out)
	}
}

func TestTeamMessageRequiresRootAndBody(t *testing.T) {
	setConfigEnv(t)
	for _, args := range [][]string{
		{"team", "message"},
		{"team", "message", "ao-100"},
		{"team", "message", "ao-100", "   "},
	} {
		_, _, err := executeCLI(t, Deps{}, args...)
		if err == nil || ExitCode(err) != 2 {
			t.Fatalf("args=%v err=%v exit=%d", args, err, ExitCode(err))
		}
	}
}

func TestTeamCommandExposesListStatusAndMessage(t *testing.T) {
	cmd := newTeamCommand(&commandContext{})
	names := make([]string, 0, len(cmd.Commands()))
	for _, child := range cmd.Commands() {
		names = append(names, child.Name())
	}
	if strings.Join(names, ",") != "ls,message,status" {
		t.Fatalf("unexpected team commands: %v", names)
	}
}
