package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/siyanzhu/agentic-platform/executor/internal/executor"
	"github.com/siyanzhu/agentic-platform/executor/internal/server"
)

func TestHealthzIdle(t *testing.T) {
	sm := executor.NewStateMachine()
	srv := server.New(sm, nil)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("healthz status = %d, want 200", w.Code)
	}
}

func TestHealthzBusy(t *testing.T) {
	sm := executor.NewStateMachine()
	_ = sm.Transition(executor.Starting)
	srv := server.New(sm, nil)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("healthz status = %d, want 503", w.Code)
	}
}

func TestRunBusy(t *testing.T) {
	sm := executor.NewStateMachine()
	_ = sm.Transition(executor.Starting)
	srv := server.New(sm, nil)

	req := httptest.NewRequest(http.MethodPost, "/run", strings.NewReader("{}"))
	req.Header.Set("X-Claim-Id", "clm-123")
	req.Header.Set("X-Execution-Id", "exec-456")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("run status = %d, want 503", w.Code)
	}
}

func TestRunMissingHeaders(t *testing.T) {
	sm := executor.NewStateMachine()
	srv := server.New(sm, nil)

	req := httptest.NewRequest(http.MethodPost, "/run", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("run status = %d, want 400", w.Code)
	}
}

func TestRunMissingExecutionId(t *testing.T) {
	sm := executor.NewStateMachine()
	srv := server.New(sm, nil)

	req := httptest.NewRequest(http.MethodPost, "/run", strings.NewReader("{}"))
	req.Header.Set("X-Claim-Id", "clm-123")
	// No X-Execution-Id
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("run status = %d, want 400", w.Code)
	}
}

func TestRunNoRunner(t *testing.T) {
	sm := executor.NewStateMachine()
	srv := server.New(sm, nil)

	req := httptest.NewRequest(http.MethodPost, "/run", strings.NewReader("{}"))
	req.Header.Set("X-Claim-Id", "clm-123")
	req.Header.Set("X-Execution-Id", "exec-456")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("run status = %d, want 500", w.Code)
	}
}

func TestStatus(t *testing.T) {
	sm := executor.NewStateMachine()
	srv := server.New(sm, nil)

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status code = %d, want 200", w.Code)
	}

	var body map[string]string
	json.NewDecoder(w.Body).Decode(&body)
	if body["state"] != "IDLE" {
		t.Errorf("state = %q, want %q", body["state"], "IDLE")
	}
}

func TestStatusWhileRunning(t *testing.T) {
	sm := executor.NewStateMachine()
	_ = sm.Transition(executor.Starting)
	_ = sm.Transition(executor.Running)
	srv := server.New(sm, nil)

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	var body map[string]string
	json.NewDecoder(w.Body).Decode(&body)
	if body["state"] != "RUNNING" {
		t.Errorf("state = %q, want %q", body["state"], "RUNNING")
	}
}
