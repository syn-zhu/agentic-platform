package proxy_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/siyanzhu/agentic-platform/executor/internal/lifecycle"
	"github.com/siyanzhu/agentic-platform/executor/internal/proxy"
)

// noopPodActions implements lifecycle.PodActions with no-op methods.
type noopPodActions struct{}

func (noopPodActions) SetupInfra(_ context.Context) error              { return nil }
func (noopPodActions) BootVM(_ context.Context) error                  { return nil }
func (noopPodActions) ResumeVM(_ context.Context) error                { return nil }
func (noopPodActions) PauseVM(_ context.Context) error                 { return nil }
func (noopPodActions) StopVM(_ context.Context)                        {}
func (noopPodActions) CleanupWorkDir(_ context.Context)                {}
func (noopPodActions) ReleaseLease(_ context.Context)                  {}
func (noopPodActions) RegisterWarm(_ context.Context, _ string) error  { return nil }
func (noopPodActions) CloseAll()                                       {}

// inMemoryStore is a simple in-memory EventStore for testing.
type inMemoryStore struct {
	state    lifecycle.ExecState
	args     []any
	events   []lifecycle.Event
	prevExec *lifecycle.PreviousExecution
}

func newInMemoryStore() *inMemoryStore {
	return &inMemoryStore{state: lifecycle.ExecPending}
}

func (s *inMemoryStore) SaveState(_ context.Context, _ string, state lifecycle.ExecState, args ...any) error {
	s.state = state
	s.args = args
	return nil
}

func (s *inMemoryStore) LoadState(_ context.Context, _ string) (lifecycle.ExecState, []any, error) {
	return s.state, s.args, nil
}

func (s *inMemoryStore) AppendEvent(_ context.Context, _ string, eventType string, data map[string]any) error {
	s.events = append(s.events, lifecycle.Event{Type: eventType, Data: data})
	return nil
}

func (s *inMemoryStore) LoadPreviousExecution(_ context.Context, _ string) (*lifecycle.PreviousExecution, error) {
	return s.prevExec, nil
}

// readyPod creates a PodLifecycle advanced to Ready state
// (Uninitialized → Configuring → Idle → Booting → Ready).
func readyPod(t *testing.T) *lifecycle.PodLifecycle {
	t.Helper()
	pod := lifecycle.NewPodLifecycle(noopPodActions{})
	ctx := context.Background()

	if err := pod.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("fire TrigConfigDone (1): %v", err)
	}
	if err := pod.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("fire TrigConfigDone (2): %v", err)
	}
	if err := pod.Fire(ctx, lifecycle.TrigPrepare, "exec-1", "session-1"); err != nil {
		t.Fatalf("fire TrigPrepare: %v", err)
	}
	if err := pod.Fire(ctx, lifecycle.TrigHealthCheckOK); err != nil {
		t.Fatalf("fire TrigHealthCheckOK: %v", err)
	}
	return pod
}

// newTestExec creates an ExecutionLifecycle in Pending state.
func newTestExec(store lifecycle.EventStore) *lifecycle.ExecutionLifecycle {
	_, cancel := context.WithCancel(context.Background())
	return lifecycle.NewExecutionLifecycle(store, "exec-1", "session-1", cancel)
}

func TestHandleInboundFirstRun(t *testing.T) {
	pod := readyPod(t)
	store := newInMemoryStore()
	exec := newTestExec(store)

	h := proxy.NewHandler(pod)
	h.SetExecution(exec)

	req := httptest.NewRequest(http.MethodPost, "/run", nil)
	w := httptest.NewRecorder()
	h.HandleInbound(w, req)

	// Should return 502 (agent forwarding placeholder).
	if w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadGateway)
	}

	// Exec should have transitioned Pending → Active.
	state, err := exec.SM.State(context.Background())
	if err != nil {
		t.Fatalf("get exec state: %v", err)
	}
	if state.(lifecycle.ExecState) != lifecycle.ExecActive {
		t.Errorf("exec state = %s, want Active", state)
	}

	// Pod should be in Executing (Ready → Executing via RunArrived).
	podState, err := pod.State(context.Background())
	if err != nil {
		t.Fatalf("get pod state: %v", err)
	}
	if podState != lifecycle.PodExecuting {
		t.Errorf("pod state = %s, want Executing", podState)
	}
}

func TestHandleInboundNoExecution(t *testing.T) {
	pod := readyPod(t)
	h := proxy.NewHandler(pod)
	// No execution wired.

	req := httptest.NewRequest(http.MethodPost, "/run", nil)
	w := httptest.NewRecorder()
	h.HandleInbound(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestHandleInboundReentrant(t *testing.T) {
	pod := readyPod(t)
	store := newInMemoryStore()
	exec := newTestExec(store)

	h := proxy.NewHandler(pod)
	h.SetExecution(exec)

	// First request: Pending → Active, Ready → Executing.
	req1 := httptest.NewRequest(http.MethodPost, "/run", nil)
	w1 := httptest.NewRecorder()
	h.HandleInbound(w1, req1)

	if w1.Code != http.StatusBadGateway {
		t.Fatalf("first request status = %d, want %d", w1.Code, http.StatusBadGateway)
	}

	// Second request while Active → ReentrantIn.
	req2 := httptest.NewRequest(http.MethodPost, "/run", nil)
	w2 := httptest.NewRecorder()
	h.HandleInbound(w2, req2)

	if w2.Code != http.StatusBadGateway {
		t.Errorf("second request status = %d, want %d", w2.Code, http.StatusBadGateway)
	}

	// inflightOps should have incremented from the re-entrant call.
	if exec.InflightOps() != 1 {
		t.Errorf("inflightOps = %d, want 1", exec.InflightOps())
	}
}

func TestHandleOutboundFiresTriggers(t *testing.T) {
	pod := readyPod(t)
	store := newInMemoryStore()
	exec := newTestExec(store)

	// Advance exec to Active so ToolCallOut is permitted.
	if err := exec.SM.FireCtx(context.Background(), lifecycle.ExecTrigRunReceived); err != nil {
		t.Fatalf("RunReceived failed: %v", err)
	}

	h := proxy.NewHandler(pod)
	h.SetExecution(exec)

	// Backend server that returns 200.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend", "true")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "backend response")
	}))
	defer backend.Close()

	req := httptest.NewRequest(http.MethodGet, backend.URL+"/tool", nil)
	w := httptest.NewRecorder()
	h.HandleOutbound(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}

	body, _ := io.ReadAll(w.Body)
	if string(body) != "backend response" {
		t.Errorf("body = %q, want %q", string(body), "backend response")
	}

	// ToolCallOut + ToolCallIn should cancel out, inflightOps = 0.
	if exec.InflightOps() != 0 {
		t.Errorf("inflightOps = %d, want 0", exec.InflightOps())
	}

	// Verify events were recorded.
	hasOut := false
	hasIn := false
	for _, evt := range store.events {
		if evt.Type == string(lifecycle.ExecTrigToolCallOut) {
			hasOut = true
		}
		if evt.Type == string(lifecycle.ExecTrigToolCallIn) {
			hasIn = true
		}
	}
	if !hasOut {
		t.Error("expected ToolCallOut event in store")
	}
	if !hasIn {
		t.Error("expected ToolCallIn event in store")
	}
}

func TestHandleOutboundNoExecution(t *testing.T) {
	pod := readyPod(t)
	h := proxy.NewHandler(pod)
	// No execution wired — should forward without recording.

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "passthrough")
	}))
	defer backend.Close()

	req := httptest.NewRequest(http.MethodGet, backend.URL+"/tool", nil)
	w := httptest.NewRecorder()
	h.HandleOutbound(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}

	body, _ := io.ReadAll(w.Body)
	if string(body) != "passthrough" {
		t.Errorf("body = %q, want %q", string(body), "passthrough")
	}
}

func TestHandleOutboundCacheHit(t *testing.T) {
	pod := readyPod(t)
	store := newInMemoryStore()
	exec := newTestExec(store)

	// Advance to Active.
	if err := exec.SM.FireCtx(context.Background(), lifecycle.ExecTrigRunReceived); err != nil {
		t.Fatalf("RunReceived failed: %v", err)
	}

	// Populate cache manually.
	exec.CachedToolResults = map[string]map[string]any{
		"http://tool-a/run": {
			"status_code": 200,
			"body":        "cached body",
		},
	}

	h := proxy.NewHandler(pod)
	h.SetExecution(exec)

	req := httptest.NewRequest(http.MethodGet, "http://tool-a/run", nil)
	w := httptest.NewRecorder()
	h.HandleOutbound(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}

	body, _ := io.ReadAll(w.Body)
	if string(body) != "cached body" {
		t.Errorf("body = %q, want %q", string(body), "cached body")
	}

	// inflightOps should stay 0 — cache hit bypasses ToolCallOut/In.
	if exec.InflightOps() != 0 {
		t.Errorf("inflightOps = %d, want 0 (cache hit should not fire triggers)", exec.InflightOps())
	}
}
