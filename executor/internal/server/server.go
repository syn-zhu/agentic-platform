package server

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/siyanzhu/agentic-platform/executor/internal/executor"
)

// Runner is called by the /run handler to execute a request.
// It blocks until the execution completes, writing SSE chunks to w.
type Runner interface {
	Run(w http.ResponseWriter, claimID, execID, sessionID string, warm bool, payload io.Reader) error
}

// Server is the executor HTTP server.
type Server struct {
	mux    *http.ServeMux
	sm     *executor.StateMachine
	runner Runner
}

// New creates an HTTP server with the executor state machine and runner.
func New(sm *executor.StateMachine, runner Runner) *Server {
	s := &Server{
		mux:    http.NewServeMux(),
		sm:     sm,
		runner: runner,
	}
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("POST /run", s.handleRun)
	s.mux.HandleFunc("GET /status", s.handleStatus)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if !s.sm.IsAvailable() {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	claimID := r.Header.Get("X-Claim-Id")
	execID := r.Header.Get("X-Execution-Id")
	sessionID := r.Header.Get("X-Session-Id")

	if claimID == "" || execID == "" || sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "X-Claim-Id, X-Execution-Id, and X-Session-Id headers required",
		})
		return
	}

	warm := r.Header.Get("X-Warm") == "true"

	if !s.sm.IsAvailable() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "executor busy",
		})
		return
	}

	if s.runner == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "no runner configured",
		})
		return
	}

	if err := s.runner.Run(w, claimID, execID, sessionID, warm, r.Body); err != nil {
		// TODO: handle the case where SSE streaming has already started —
		// once headers are flushed we can't change the status code.
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": err.Error(),
		})
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	state := s.sm.State()
	resp := map[string]string{
		"state": state.String(),
	}
	writeJSON(w, http.StatusOK, resp)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
