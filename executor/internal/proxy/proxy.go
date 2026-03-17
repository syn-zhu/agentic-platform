//go:build linux

package proxy

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"

	"github.com/siyanzhu/agentic-platform/executor/internal/lifecycle"
)

// Proxy is a transparent HTTP forward proxy that intercepts outbound
// connections redirected by the eBPF connect4 program. It delegates request
// handling to the SM-integrated Handler.
type Proxy struct {
	listener    net.Listener
	interceptor *EBPFInterceptor
	handler     *Handler

	server    *http.Server
	closeOnce sync.Once
}

// Config holds proxy configuration.
type Config struct {
	// ListenAddr is the proxy listen address (default "127.0.0.1:3128").
	ListenAddr string

	// BPFObjPath is the path to the compiled eBPF object file.
	BPFObjPath string

	// CgroupPath is the cgroup where the eBPF program is attached.
	CgroupPath string

	// Pod is the pod lifecycle state machine.
	Pod *lifecycle.PodLifecycle
}

// New creates and starts the proxy.
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
		handler:     NewHandler(cfg.Pod),
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

// SetExecution wires an execution lifecycle into the proxy handler.
// This implements the server.Proxy interface.
func (p *Proxy) SetExecution(exec *lifecycle.ExecutionLifecycle) {
	p.handler.SetExecution(exec)
}

// Close shuts down the proxy and detaches the eBPF program.
func (p *Proxy) Close() {
	p.closeOnce.Do(func() {
		slog.Info("closing proxy")
		p.server.Shutdown(context.Background())
		p.listener.Close()
		p.interceptor.Close()
	})
}

// handleConnect handles both HTTP and HTTPS (CONNECT) requests.
func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.handler.HandleOutboundCONNECT(w, r)
		return
	}
	p.handler.HandleOutbound(w, r)
}
