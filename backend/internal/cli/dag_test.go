package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const dagRunFixture = `{
  "name": "feature-x",
  "nodes": [
    {"id": "build", "name": "Build API", "prompt": "Implement the endpoint.", "harness": "claude-code"},
    {"id": "test", "name": "Test API", "prompt": "Add integration tests.", "dependsOn": ["build"], "requiredState": "completed"}
  ]
}`

const dagRunEnvelopeFixture = `{"run":{"id":"dag-1","projectId":"p","name":"feature-x","status":"pending","counts":{"total":2,"queued":1,"blocked":1},"nodes":[{"id":"build","sessionId":"p-1","name":"Build API","status":"queued"},{"id":"test","sessionId":"p-2","name":"Test API","status":"blocked","blockedReason":"dependency_pending"}]}}`

func dagTestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/telemetry/cli-invoked" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func dagProjectResponse(w http.ResponseWriter) {
	_ = json.NewEncoder(w).Encode(projectGetResult{Status: "ok", Project: projectDetails{ID: "p"}})
}

func TestDagRunSubmitsFile(t *testing.T) {
	cfg := setConfigEnv(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "run.json")
	if err := os.WriteFile(file, []byte(dagRunFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	var submitted dagRunSubmitRequest
	srv := dagTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/p":
			dagProjectResponse(w)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/p/dag-runs":
			if err := json.NewDecoder(r.Body).Decode(&submitted); err != nil {
				t.Errorf("decode submission: %v", err)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(dagRunEnvelopeFixture))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	writeRunFileFor(t, cfg, srv)

	out, stderr, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "dag", "run", "--project", "p", "--file", file)
	if err != nil {
		t.Fatalf("dag run: %v stderr=%s", err, stderr)
	}
	if submitted.Name != "feature-x" || len(submitted.Nodes) != 2 {
		t.Fatalf("unexpected submission: %#v", submitted)
	}
	if got := submitted.Nodes[1]; got.ID != "test" || len(got.DependsOn) != 1 || got.DependsOn[0] != "build" || got.RequiredState != "completed" {
		t.Fatalf("dependency edge lost: %#v", got)
	}
	for _, want := range []string{"run dag-1  pending  feature-x", "2 total, 1 queued, 1 blocked", "dependency_pending"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestDagRunReadsStdinAndJSONOutput(t *testing.T) {
	cfg := setConfigEnv(t)
	requests := 0
	srv := dagTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path == "/api/v1/projects/p" {
			dagProjectResponse(w)
			return
		}
		if r.URL.Path != "/api/v1/projects/p/dag-runs" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(dagRunEnvelopeFixture))
	})
	writeRunFileFor(t, cfg, srv)

	out, stderr, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
		In:           strings.NewReader(dagRunFixture),
	}, "dag", "run", "--project", "p", "--file", "-", "--json")
	if err != nil {
		t.Fatalf("dag run from stdin: %v stderr=%s", err, stderr)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope) != 1 || envelope["run"] == nil {
		t.Fatalf("unexpected JSON envelope: %s", out)
	}
}

func TestDagRunRejectsBadInputBeforeCalling(t *testing.T) {
	cfg := setConfigEnv(t)
	dir := t.TempDir()
	unknown := filepath.Join(dir, "unknown.json")
	if err := os.WriteFile(unknown, []byte(`{"name":"x","nodes":[{"id":"a","prompt":"p","surprise":true}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(empty, []byte(`{"name":"x","nodes":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	requests := 0
	srv := dagTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.NotFound(w, r)
	})
	writeRunFileFor(t, cfg, srv)

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"missing file flag", []string{"dag", "run", "--project", "p"}, "--file is required"},
		{"unknown node field", []string{"dag", "run", "--project", "p", "--file", unknown}, "unknown field"},
		{"no nodes", []string{"dag", "run", "--project", "p", "--file", empty}, "nodes is required"},
		{"missing run id", []string{"dag", "status"}, "accepts 1 arg"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if ExitCode(err) != 2 {
				t.Fatalf("exit code = %d, want 2", ExitCode(err))
			}
		})
	}
	if requests != 0 {
		t.Fatalf("invalid input reached the daemon %d times", requests)
	}
}

func TestDagStatusAndCancel(t *testing.T) {
	cfg := setConfigEnv(t)
	var cancelBody dagCancelRequest
	srv := dagTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/dag-runs/dag-1":
			_, _ = w.Write([]byte(dagRunEnvelopeFixture))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/dag-runs/dag-1/cancel":
			if err := json.NewDecoder(r.Body).Decode(&cancelBody); err != nil {
				t.Errorf("decode cancel: %v", err)
			}
			_, _ = w.Write([]byte(`{"run":{"id":"dag-1","name":"feature-x","status":"cancelled","counts":{"total":2,"cancelled":2}}}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	writeRunFileFor(t, cfg, srv)

	out, stderr, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "dag", "status", "dag-1")
	if err != nil {
		t.Fatalf("dag status: %v stderr=%s", err, stderr)
	}
	if !strings.Contains(out, "run dag-1  pending  feature-x") {
		t.Fatalf("status output: %s", out)
	}

	out, stderr, err = executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "dag", "cancel", "dag-1", "--include-active")
	if err != nil {
		t.Fatalf("dag cancel: %v stderr=%s", err, stderr)
	}
	if !cancelBody.IncludeActive {
		t.Fatal("includeActive was not forwarded")
	}
	if !strings.Contains(out, "run dag-1 cancelled") || !strings.Contains(out, "2 total, 0 queued") {
		t.Fatalf("cancel output: %s", out)
	}
}

func TestDagSurfacesDaemonErrorEnvelope(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := dagTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"unknown node","code":"DAG_UNKNOWN_NODE","requestId":"req-dag"}`))
	})
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "dag", "status", "dag-9")
	if err == nil || !strings.Contains(err.Error(), "unknown node (DAG_UNKNOWN_NODE) [request req-dag]") {
		t.Fatalf("err = %v", err)
	}
	if ExitCode(err) != 1 {
		t.Fatalf("exit code = %d, want 1", ExitCode(err))
	}
}
