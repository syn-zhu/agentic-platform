// pool-operator/internal/server/server.go
package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/siyanzhu/agentic-platform/pool-operator/internal/pool"
)

// ClaimRequest is the JSON body for POST /claim.
type ClaimRequest struct {
	Pool string `json:"pool"`
}

// ClaimResponse is returned on a successful claim.
type ClaimResponse struct {
	PodName string `json:"pod_name"`
	PodIP   string `json:"pod_ip"`
	PodPort int32  `json:"pod_port"`
	ClaimID string `json:"claim_id"`
}

// ExhaustedResponse is returned when no pods are available.
type ExhaustedResponse struct {
	Error     string `json:"error"`
	Available int    `json:"available"`
	Warming   int    `json:"warming"`
}

// RenewRequest is the JSON body for POST /renew.
type RenewRequest struct {
	ClaimID string `json:"claim_id"`
}

// RenewResponse is returned on a successful renew.
type RenewResponse struct {
	ExpiresAt time.Time `json:"expires_at"`
}

// ReleaseRequest is the JSON body for POST /release.
type ReleaseRequest struct {
	ClaimID string `json:"claim_id"`
}

// ReleaseResponse is returned on a successful release.
type ReleaseResponse struct {
	Status string `json:"status"`
}

// StatusResponse is returned by GET /status.
type StatusResponse struct {
	Pools map[string]pool.PoolStatus `json:"pools"`
}

// ErrorResponse is a generic error response.
type ErrorResponse struct {
	Error string `json:"error"`
}

// Server is the HTTP server that exposes pool operations.
type Server struct {
	registry *pool.Registry
	metrics  *Metrics
}

// New creates a new Server backed by the given registry.
func New(registry *pool.Registry) *Server {
	return &Server{
		registry: registry,
		metrics:  NewMetrics(),
	}
}

// Metrics returns the server's Metrics instance for use by other components.
func (s *Server) Metrics() *Metrics {
	return s.metrics
}

// Handler returns an http.Handler with all routes registered.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /claim", s.handleClaim)
	mux.HandleFunc("POST /renew", s.handleRenew)
	mux.HandleFunc("POST /release", s.handleRelease)
	mux.HandleFunc("GET /status", s.handleStatus)
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	return mux
}

func (s *Server) handleClaim(w http.ResponseWriter, r *http.Request) {
	var req ClaimRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid request body"})
		return
	}

	p := s.registry.Get(req.Pool)
	if p == nil {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "pool not found"})
		return
	}

	start := time.Now()
	claim, err := p.Claim()
	duration := time.Since(start)

	s.metrics.ClaimDuration.WithLabelValues(req.Pool).Observe(duration.Seconds())

	if err != nil {
		if errors.Is(err, pool.ErrPoolExhausted) {
			s.metrics.ExhaustedTotal.WithLabelValues(req.Pool).Inc()
			status := p.Status()
			writeJSON(w, http.StatusServiceUnavailable, ExhaustedResponse{
				Error:     "no available pods",
				Available: status.Available,
				Warming:   status.Warming,
			})
			return
		}
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	s.metrics.ClaimTotal.WithLabelValues(req.Pool).Inc()
	writeJSON(w, http.StatusOK, ClaimResponse{
		PodName: claim.PodInfo.Name,
		PodIP:   claim.PodInfo.IP,
		PodPort: claim.PodInfo.Port,
		ClaimID: claim.ClaimID,
	})
}

func (s *Server) handleRenew(w http.ResponseWriter, r *http.Request) {
	var req RenewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid request body"})
		return
	}

	var expiresAt time.Time
	var found bool
	for _, p := range s.registry.List() {
		t, err := p.Renew(req.ClaimID)
		if err == nil {
			expiresAt = t
			found = true
			break
		}
	}

	if !found {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "claim not found"})
		return
	}

	writeJSON(w, http.StatusOK, RenewResponse{ExpiresAt: expiresAt})
}

func (s *Server) handleRelease(w http.ResponseWriter, r *http.Request) {
	var req ReleaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid request body"})
		return
	}

	var found bool
	for _, p := range s.registry.List() {
		_, err := p.Release(req.ClaimID)
		if err == nil {
			found = true
			break
		}
	}

	if !found {
		writeJSON(w, http.StatusNotFound, ErrorResponse{Error: "claim not found"})
		return
	}

	writeJSON(w, http.StatusOK, ReleaseResponse{Status: "released"})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	pools := s.registry.List()
	resp := StatusResponse{Pools: make(map[string]pool.PoolStatus, len(pools))}
	for _, p := range pools {
		resp.Pools[p.Name()] = p.Status()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
