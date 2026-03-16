//go:build linux

package proxy

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
)

// Proxy is a transparent HTTP forward proxy that intercepts outbound
// connections redirected by the eBPF connect4 program. It logs
// requests/responses and provides a replay cache for idempotent resume.
type Proxy struct {
	listener    net.Listener
	interceptor *EBPFInterceptor
	store       EventStore
	sessionID   string
	step        atomic.Int64

	server *http.Server
	once   sync.Once
}

// Config holds proxy configuration.
type Config struct {
	// ListenAddr is the proxy listen address (default "127.0.0.1:3128").
	ListenAddr string

	// BPFObjPath is the path to the compiled eBPF object file.
	BPFObjPath string

	// CgroupPath is the cgroup where the eBPF program is attached.
	CgroupPath string

	// Store persists event logs and serves the replay cache.
	Store EventStore

	// SessionID is the current execution's session ID.
	SessionID string
}

// New creates and starts the proxy. It loads the eBPF program,
// attaches it to the cgroup, and starts listening for redirected connections.
func New(cfg *Config) (*Proxy, error) {
	addr := cfg.ListenAddr
	if addr == "" {
		addr = "127.0.0.1:3128"
	}

	interceptor, err := LoadEBPF(cfg.CgroupPath, cfg.BPFObjPath)
	if err != nil {
		return nil, fmt.Errorf("load eBPF: %w", err)
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		interceptor.Close()
		return nil, fmt.Errorf("listen on %s: %w", addr, err)
	}

	p := &Proxy{
		listener:    ln,
		interceptor: interceptor,
		store:       cfg.Store,
		sessionID:   cfg.SessionID,
	}

	p.server = &http.Server{
		Handler: http.HandlerFunc(p.handleConnect),
	}

	go func() {
		slog.Info("proxy listening", "addr", addr)
		if err := p.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Error("proxy serve error", "error", err)
		}
	}()

	return p, nil
}

// SetSessionID updates the session ID for step counting and replay cache.
func (p *Proxy) SetSessionID(sessionID string) {
	p.sessionID = sessionID
	p.step.Store(0)
}

// Close shuts down the proxy and detaches the eBPF program.
func (p *Proxy) Close() {
	p.once.Do(func() {
		slog.Info("closing proxy")
		p.server.Shutdown(context.Background())
		p.listener.Close()
		p.interceptor.Close()
	})
}

// handleConnect handles both HTTP and HTTPS (CONNECT) requests.
func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.handleHTTPS(w, r)
		return
	}
	p.handleHTTP(w, r)
}

// handleHTTP proxies a plain HTTP request with logging.
func (p *Proxy) handleHTTP(w http.ResponseWriter, r *http.Request) {
	step := int(p.step.Add(1))
	log := slog.With("session", p.sessionID, "step", step, "url", r.URL.String())

	// Check replay cache.
	if p.store != nil && p.sessionID != "" {
		cached, err := p.store.GetCachedResponse(r.Context(), p.sessionID, step)
		if err == nil && cached != nil {
			log.Info("replay cache hit")
			for k, vs := range cached.Header {
				for _, v := range vs {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(cached.StatusCode)
			io.Copy(w, cached.Body)
			cached.Body.Close()
			return
		}
	}

	// Log request.
	if p.store != nil {
		p.store.LogRequest(r.Context(), p.sessionID, step, r)
	}

	// Forward request.
	log.Info("forwarding request")
	resp, err := http.DefaultTransport.RoundTrip(r)
	if err != nil {
		log.Error("forward failed", "error", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Log response.
	if p.store != nil {
		p.store.LogResponse(r.Context(), p.sessionID, step, resp)
	}

	// Copy response to client.
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// handleHTTPS handles CONNECT tunneling for TLS traffic.
// We tunnel the raw bytes — we can log the connection metadata
// (destination host:port) but not the encrypted payload.
func (p *Proxy) handleHTTPS(w http.ResponseWriter, r *http.Request) {
	step := int(p.step.Add(1))
	log := slog.With("session", p.sessionID, "step", step, "host", r.Host)

	// Log the CONNECT (we can't see the encrypted payload).
	if p.store != nil {
		p.store.LogRequest(r.Context(), p.sessionID, step, r)
	}

	log.Info("tunneling HTTPS")

	// Connect to the real destination.
	destConn, err := net.Dial("tcp", r.Host)
	if err != nil {
		log.Error("tunnel dial failed", "error", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	// Tell the client the tunnel is established.
	w.WriteHeader(http.StatusOK)

	// Hijack the client connection for raw TCP tunneling.
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		log.Error("response writer does not support hijack")
		destConn.Close()
		return
	}

	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		log.Error("hijack failed", "error", err)
		destConn.Close()
		return
	}

	// Bidirectional copy.
	go func() {
		io.Copy(destConn, clientConn)
		destConn.Close()
	}()
	go func() {
		io.Copy(clientConn, destConn)
		clientConn.Close()
	}()
}
