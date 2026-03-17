package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/siyanzhu/agentic-platform/executor/internal/lifecycle"
	"github.com/siyanzhu/agentic-platform/executor/internal/server"
)

// noopActions implements lifecycle.PodActions with no-op methods.
type noopActions struct{}

func (noopActions) SetupInfra(_ context.Context) error              { return nil }
func (noopActions) BootVM(_ context.Context) error                  { return nil }
func (noopActions) ResumeVM(_ context.Context) error                { return nil }
func (noopActions) PauseVM(_ context.Context) error                 { return nil }
func (noopActions) StopVM(_ context.Context)                        {}
func (noopActions) CleanupWorkDir(_ context.Context)                {}
func (noopActions) ReleaseLease(_ context.Context)                  {}
func (noopActions) RegisterWarm(_ context.Context, _ string) error  { return nil }
func (noopActions) CloseAll()                                       {}

// mockProxy records whether SetExecution was called and what was passed.
type mockProxy struct {
	called bool
	exec   *lifecycle.ExecutionLifecycle
}

func (m *mockProxy) SetExecution(exec *lifecycle.ExecutionLifecycle) {
	m.called = true
	m.exec = exec
}

// newTestServer creates a PodLifecycle advanced to Idle (via two TrigConfigDone
// fires: Uninitialized → Configuring → Idle) and returns a ready-to-use server.
func newTestServer(t *testing.T) (*server.Server, *mockProxy, *lifecycle.PodLifecycle) {
	t.Helper()

	pod := lifecycle.NewPodLifecycle(noopActions{})
	ctx := context.Background()

	// Uninitialized → Configuring
	if err := pod.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("fire TrigConfigDone (1): %v", err)
	}
	// Configuring → Idle
	if err := pod.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("fire TrigConfigDone (2): %v", err)
	}

	proxy := &mockProxy{}
	store := lifecycle.NoopEventStore{}
	cfg := server.PrepareConfig{}

	srv := server.New(pod, proxy, store, cfg)
	return srv, proxy, pod
}

func TestHealthzIdle(t *testing.T) {
	srv, _, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("healthz status = %d, want 200", w.Code)
	}
}

func TestHealthzBusy(t *testing.T) {
	srv, _, pod := newTestServer(t)

	// Advance pod past Idle by firing Prepare (Idle → Booting).
	ctx := context.Background()
	if err := pod.Fire(ctx, lifecycle.TrigPrepare, "exec-1", "sess-1"); err != nil {
		t.Fatalf("fire TrigPrepare: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("healthz status = %d, want 503", w.Code)
	}
}

func TestPrepareMissingHeader(t *testing.T) {
	srv, _, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/prepare", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("prepare status = %d, want 400", w.Code)
	}
}

func TestPrepareSuccess(t *testing.T) {
	srv, proxy, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/prepare", nil)
	req.Header.Set("X-Execution-Id", "exec-123")
	req.Header.Set("X-Session-Id", "sess-456")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("prepare status = %d, want 202", w.Code)
	}
	if !proxy.called {
		t.Error("proxy.SetExecution was not called")
	}
	if proxy.exec == nil {
		t.Error("proxy.SetExecution received nil execution")
	}
}

func TestPrepareBusy(t *testing.T) {
	srv, _, _ := newTestServer(t)

	// First prepare succeeds.
	req1 := httptest.NewRequest(http.MethodPost, "/prepare", nil)
	req1.Header.Set("X-Execution-Id", "exec-1")
	w1 := httptest.NewRecorder()
	srv.ServeHTTP(w1, req1)

	if w1.Code != http.StatusAccepted {
		t.Fatalf("first prepare status = %d, want 202", w1.Code)
	}

	// Second prepare returns 409 (pod is no longer Idle).
	req2 := httptest.NewRequest(http.MethodPost, "/prepare", nil)
	req2.Header.Set("X-Execution-Id", "exec-2")
	w2 := httptest.NewRecorder()
	srv.ServeHTTP(w2, req2)

	if w2.Code != http.StatusConflict {
		t.Errorf("second prepare status = %d, want 409", w2.Code)
	}
}

func TestStatus(t *testing.T) {
	srv, _, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status code = %d, want 200", w.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode status body: %v", err)
	}
	if body["state"] != "Idle" {
		t.Errorf("state = %q, want %q", body["state"], "Idle")
	}
}
