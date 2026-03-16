// Package server implements the HTTP API for the assignment service.
//
// The assignment service sits in the request path as a pre-routing hook.
// The waypoint (via ExtProc) calls it to get a pod assignment before
// forwarding the request to the executor.
package server

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/siyanzhu/agentic-platform/assignment/internal/pool"
)

// Server is the assignment service HTTP server.
type Server struct {
	pool   *pool.Pool
	logger *slog.Logger
}

// New creates a new Server.
func New(p *pool.Pool, logger *slog.Logger) *Server {
	return &Server{pool: p, logger: logger}
}

// AssignRequest is the request body for POST /assign.
type AssignRequest struct {
	// TemplateHash identifies the agent image / workload type.
	TemplateHash string `json:"template_hash"`
}

// AssignResponse is the response body for POST /assign.
type AssignResponse struct {
	// PodAddr is the address (ip:port) of the assigned executor pod.
	PodAddr string `json:"pod_addr"`
}

// ErrorResponse is the response body for errors.
type ErrorResponse struct {
	Error string `json:"error"`
}

// RegisterRequest is the request body for POST /register, /deregister, /heartbeat.
type RegisterRequest struct {
	TemplateHash string `json:"template_hash"`
	PodAddr      string `json:"pod_addr"`
}

// Handler returns the HTTP handler for the assignment service.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /assign", s.handleAssign)
	mux.HandleFunc("POST /register", s.handleRegister)
	mux.HandleFunc("POST /deregister", s.handleDeregister)
	mux.HandleFunc("POST /heartbeat", s.handleHeartbeat)
	mux.HandleFunc("GET /health", s.handleHealth)
	return mux
}

// handleAssign claims an idle executor pod for the given template hash.
// Single Redis round-trip via Lua script (prune stale + ZPOPMIN).
func (s *Server) handleAssign(w http.ResponseWriter, r *http.Request) {
	var req AssignRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.TemplateHash == "" {
		s.writeError(w, http.StatusBadRequest, "template_hash is required")
		return
	}

	podAddr, err := s.pool.Claim(r.Context(), req.TemplateHash)
	if err != nil {
		s.logger.Error("failed to claim pod", "error", err, "template_hash", req.TemplateHash)
		s.writeError(w, http.StatusInternalServerError, "assignment failed")
		return
	}

	if podAddr == "" {
		s.logger.Warn("no idle pods available", "template_hash", req.TemplateHash)
		s.writeError(w, http.StatusServiceUnavailable, "no idle pods available")
		return
	}

	s.logger.Info("assigned pod", "pod_addr", podAddr, "template_hash", req.TemplateHash)
	s.writeJSON(w, http.StatusOK, AssignResponse{PodAddr: podAddr})
}

// handleRegister adds a pod to the idle pool. Called by executor pods.
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.TemplateHash == "" || req.PodAddr == "" {
		s.writeError(w, http.StatusBadRequest, "template_hash and pod_addr are required")
		return
	}

	if err := s.pool.Register(r.Context(), req.TemplateHash, req.PodAddr); err != nil {
		s.logger.Error("failed to register pod", "error", err)
		s.writeError(w, http.StatusInternalServerError, "registration failed")
		return
	}

	s.logger.Info("registered pod", "pod_addr", req.PodAddr, "template_hash", req.TemplateHash)
	w.WriteHeader(http.StatusNoContent)
}

// handleDeregister removes a pod from the idle pool. Called by executor pods on graceful shutdown.
func (s *Server) handleDeregister(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.TemplateHash == "" || req.PodAddr == "" {
		s.writeError(w, http.StatusBadRequest, "template_hash and pod_addr are required")
		return
	}

	if err := s.pool.Deregister(r.Context(), req.TemplateHash, req.PodAddr); err != nil {
		s.logger.Error("failed to deregister pod", "error", err)
		s.writeError(w, http.StatusInternalServerError, "deregistration failed")
		return
	}

	s.logger.Info("deregistered pod", "pod_addr", req.PodAddr, "template_hash", req.TemplateHash)
	w.WriteHeader(http.StatusNoContent)
}

// handleHeartbeat refreshes a pod's score in the sorted set. Called by executor pods periodically.
func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.TemplateHash == "" || req.PodAddr == "" {
		s.writeError(w, http.StatusBadRequest, "template_hash and pod_addr are required")
		return
	}

	if err := s.pool.Heartbeat(r.Context(), req.TemplateHash, req.PodAddr); err != nil {
		s.logger.Error("failed to refresh heartbeat", "error", err)
		s.writeError(w, http.StatusInternalServerError, "heartbeat failed")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleHealth is a simple liveness probe.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		s.logger.Error("failed to encode response", "error", err)
	}
}

func (s *Server) writeError(w http.ResponseWriter, status int, msg string) {
	s.writeJSON(w, status, ErrorResponse{Error: msg})
}
