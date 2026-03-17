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
)

// Proxy is a transparent HTTP forward proxy that intercepts outbound
// connections redirected by the eBPF connect4 program. It logs
// requests/responses via the ExecutionSerializer.
type Proxy struct {
	listener    net.Listener
	interceptor *EBPFInterceptor
	serializer  *ExecutionSerializer

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

	// Serializer is the shared execution serializer for event persistence.
	Serializer *ExecutionSerializer
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
		serializer:  cfg.Serializer,
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
// Blocks until the serializer's gate is open (execution_start persisted).
func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.handleHTTPS(w, r)
		return
	}
	p.handleHTTP(w, r)
}

// handleHTTP proxies a plain HTTP request. Records each request/response
// as an execution step via the serializer (blocks until persisted).
func (p *Proxy) handleHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Record request — blocks until persisted.
	p.serializer.RecordExecutionStep(ctx, EventToolRequest,
		FormatRequestData(r.Method, r.URL.String(), flattenHeaders(r.Header)))

	// Forward request.
	resp, err := http.DefaultTransport.RoundTrip(r)
	if err != nil {
		slog.Error("forward failed", "error", err)
		p.serializer.RecordExecutionStep(ctx, EventError, FormatError(err))
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Record response — blocks until persisted.
	p.serializer.RecordExecutionStep(ctx, EventToolResponse,
		FormatResponseData(resp.StatusCode, flattenHeaders(resp.Header)))

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
func (p *Proxy) handleHTTPS(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Record the CONNECT.
	p.serializer.RecordExecutionStep(ctx, EventToolRequest,
		FormatRequestData("CONNECT", r.Host, flattenHeaders(r.Header)))

	// Connect to the real destination.
	destConn, err := net.Dial("tcp", r.Host)
	if err != nil {
		slog.Error("tunnel dial failed", "error", err)
		p.serializer.RecordExecutionStep(ctx, EventError, FormatError(err))
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	w.WriteHeader(http.StatusOK)

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		slog.Error("response writer does not support hijack")
		destConn.Close()
		return
	}

	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		slog.Error("hijack failed", "error", err)
		destConn.Close()
		return
	}

	// Bidirectional copy.
	done := make(chan struct{}, 2)
	go func() {
		io.Copy(destConn, clientConn)
		destConn.Close()
		done <- struct{}{}
	}()
	go func() {
		io.Copy(clientConn, destConn)
		clientConn.Close()
		done <- struct{}{}
	}()
	<-done
	<-done
}

// flattenHeaders converts http.Header to a simple map (first value only).
func flattenHeaders(h http.Header) map[string]string {
	m := make(map[string]string, len(h))
	for k, vs := range h {
		if len(vs) > 0 {
			m[k] = vs[0]
		}
	}
	return m
}
