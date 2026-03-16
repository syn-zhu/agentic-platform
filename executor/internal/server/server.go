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
	Run(w http.ResponseWriter, claimID, execID string, payload io.Reader) error
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
	if !s.sm.IsIdle() {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	claimID := r.Header.Get("X-Claim-Id")
	execID := r.Header.Get("X-Execution-Id")

	if claimID == "" || execID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "X-Claim-Id and X-Execution-Id headers required",
		})
		return
	}

	if !s.sm.IsIdle() {
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

	rw := &responseTracker{ResponseWriter: w}
	if err := s.runner.Run(rw, claimID, execID, r.Body); err != nil {
		if rw.written {
			// SSE headers already sent — can't change status code.
			// The client sees a truncated stream.
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": err.Error(),
		})
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"state": s.sm.State().String(),
	})
}

// responseTracker wraps http.ResponseWriter to track whether
// any bytes have been written (and thus headers flushed).
type responseTracker struct {
	http.ResponseWriter
	written bool
}

func (rt *responseTracker) Write(b []byte) (int, error) {
	rt.written = true
	return rt.ResponseWriter.Write(b)
}

func (rt *responseTracker) WriteHeader(code int) {
	rt.written = true
	rt.ResponseWriter.WriteHeader(code)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
