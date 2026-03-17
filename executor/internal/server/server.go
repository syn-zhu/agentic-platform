package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/siyanzhu/agentic-platform/executor/internal/lifecycle"
)

// PrepareConfig holds configuration for the /prepare handler.
type PrepareConfig struct {
	RunArrivalTimeout time.Duration
}

// Proxy is the interface for wiring an execution into the proxy.
type Proxy interface {
	SetExecution(exec *lifecycle.ExecutionLifecycle)
}

// Server is the executor HTTP server.
type Server struct {
	mux   *http.ServeMux
	pod   *lifecycle.PodLifecycle
	proxy Proxy
	store lifecycle.EventStore
	cfg   PrepareConfig
}

// New creates an HTTP server wired to the pod lifecycle state machine.
func New(pod *lifecycle.PodLifecycle, proxy Proxy, store lifecycle.EventStore, cfg PrepareConfig) *Server {
	s := &Server{
		mux:   http.NewServeMux(),
		pod:   pod,
		proxy: proxy,
		store: store,
		cfg:   cfg,
	}
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("POST /prepare", s.handlePrepare)
	s.mux.HandleFunc("GET /status", s.handleStatus)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	idle, err := s.pod.IsInState(lifecycle.PodIdle)
	if err != nil || !idle {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handlePrepare(w http.ResponseWriter, r *http.Request) {
	execID := r.Header.Get("X-Execution-Id")
	if execID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "X-Execution-Id header required",
		})
		return
	}
	sessionID := r.Header.Get("X-Session-Id")

	// Create an execution lifecycle and wire it into the proxy.
	ctx := r.Context()
	_, cancel := context.WithCancel(ctx)
	exec := lifecycle.NewExecutionLifecycle(s.store, execID, sessionID, cancel)
	s.proxy.SetExecution(exec)

	// Fire TrigPrepare to move pod from Idle → Booting.
	if err := s.pod.Fire(ctx, lifecycle.TrigPrepare, execID, sessionID); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": err.Error(),
		})
		return
	}

	// Fire TrigHealthCheckOK to move pod from Booting → Ready.
	if err := s.pod.Fire(ctx, lifecycle.TrigHealthCheckOK); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": err.Error(),
		})
		return
	}

	// Start /run arrival timeout in a background goroutine.
	if s.cfg.RunArrivalTimeout > 0 {
		go func() {
			time.Sleep(s.cfg.RunArrivalTimeout)
			_ = s.pod.Fire(context.Background(), lifecycle.TrigTimeout)
		}()
	}

	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	state, err := s.pod.State(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"state": string(state),
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
