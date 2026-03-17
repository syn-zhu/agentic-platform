package proxy

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"

	"github.com/siyanzhu/agentic-platform/executor/internal/lifecycle"
)

// Handler contains SM-integrated request handling logic for the proxy.
// It is platform-independent (no build tags) so it can be tested on macOS.
type Handler struct {
	pod *lifecycle.PodLifecycle

	mu   sync.RWMutex
	exec *lifecycle.ExecutionLifecycle
}

// NewHandler creates a new Handler wired to the given PodLifecycle.
func NewHandler(pod *lifecycle.PodLifecycle) *Handler {
	return &Handler{pod: pod}
}

// SetExecution wires in a new execution lifecycle for the current /run.
func (h *Handler) SetExecution(exec *lifecycle.ExecutionLifecycle) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.exec = exec
}

// getExec returns the current execution lifecycle (nil if none wired).
func (h *Handler) getExec() *lifecycle.ExecutionLifecycle {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.exec
}

// HandleInbound handles an inbound /run request from the agent SDK.
// It blocks until the pod enters the Ready/Executing state, then fires the
// appropriate SM triggers to advance the execution state.
func (h *Handler) HandleInbound(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Block until pod is ready (GateOpen superstate).
	select {
	case <-h.pod.ReadyCh():
	case <-ctx.Done():
		http.Error(w, "request cancelled while waiting for pod ready", http.StatusGatewayTimeout)
		return
	}

	exec := h.getExec()
	if exec == nil {
		http.Error(w, "no execution wired", http.StatusServiceUnavailable)
		return
	}

	// Check execution state and fire appropriate triggers.
	state, err := exec.SM.State(ctx)
	if err != nil {
		slog.Error("failed to get exec state", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	switch state.(lifecycle.ExecState) {
	case lifecycle.ExecPending:
		// First /run request — transition Pending → Active.
		if err := exec.SM.FireCtx(ctx, lifecycle.ExecTrigRunReceived); err != nil {
			slog.Error("RunReceived trigger failed", "error", err)
			http.Error(w, "failed to start execution", http.StatusInternalServerError)
			return
		}
		// Advance pod from Ready → Executing.
		if err := h.pod.Fire(ctx, lifecycle.TrigRunArrived); err != nil {
			slog.Error("RunArrived trigger failed", "error", err)
			http.Error(w, "failed to advance pod state", http.StatusInternalServerError)
			return
		}

	case lifecycle.ExecActive:
		// Re-entrant request while execution is already active.
		if err := exec.SM.FireCtx(ctx, lifecycle.ExecTrigReentrantIn); err != nil {
			slog.Error("ReentrantIn trigger failed", "error", err)
			http.Error(w, "failed to record re-entrant call", http.StatusInternalServerError)
			return
		}

	default:
		slog.Warn("inbound request in unexpected exec state", "state", state)
		http.Error(w, "execution not in a runnable state", http.StatusConflict)
		return
	}

	// TODO: forward request to agent (pasta configuration dependent).
	// For now, return 502 as a placeholder.
	http.Error(w, "agent forwarding not yet implemented", http.StatusBadGateway)
}

// HandleOutbound handles a plain HTTP outbound tool call from the agent.
// It checks the tool cache, fires ToolCallOut, forwards the request, and
// fires ToolCallIn on completion.
func (h *Handler) HandleOutbound(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	exec := h.getExec()

	// If no execution is wired, forward without recording.
	if exec == nil {
		resp, err := http.DefaultTransport.RoundTrip(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		copyResponse(w, resp)
		return
	}

	url := r.URL.String()

	// Check tool cache for a hit.
	if exec.CachedToolResults != nil {
		if cached, ok := exec.CachedToolResults[url]; ok {
			writeCachedResponse(w, cached)
			return
		}
	}

	// Fire ToolCallOut.
	if err := exec.SM.FireCtx(ctx, lifecycle.ExecTrigToolCallOut); err != nil {
		slog.Error("ToolCallOut trigger failed", "error", err)
		http.Error(w, "failed to record tool call", http.StatusInternalServerError)
		return
	}

	// Forward the request.
	resp, err := http.DefaultTransport.RoundTrip(r)
	if err != nil {
		slog.Error("outbound forward failed", "error", err)
		// Fire ToolCallError to decrement inflight ops.
		_ = exec.SM.FireCtx(ctx, lifecycle.ExecTrigToolCallError)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Fire ToolCallIn.
	if err := exec.SM.FireCtx(ctx, lifecycle.ExecTrigToolCallIn); err != nil {
		slog.Error("ToolCallIn trigger failed", "error", err)
	}

	copyResponse(w, resp)
}

// HandleOutboundCONNECT handles an HTTPS CONNECT tunnel for outbound tool
// calls. It fires ToolCallOut, establishes a bidirectional tunnel, and fires
// ToolCallIn when the tunnel closes.
func (h *Handler) HandleOutboundCONNECT(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	exec := h.getExec()

	// Fire ToolCallOut if we have an execution.
	if exec != nil {
		if err := exec.SM.FireCtx(ctx, lifecycle.ExecTrigToolCallOut); err != nil {
			slog.Error("ToolCallOut trigger failed", "error", err)
			http.Error(w, "failed to record tool call", http.StatusInternalServerError)
			return
		}
	}

	// Dial the real destination.
	destConn, err := net.Dial("tcp", r.Host)
	if err != nil {
		slog.Error("tunnel dial failed", "error", err)
		if exec != nil {
			_ = exec.SM.FireCtx(ctx, lifecycle.ExecTrigToolCallError)
		}
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

	// Fire ToolCallIn when the tunnel closes.
	if exec != nil {
		if err := exec.SM.FireCtx(context.Background(), lifecycle.ExecTrigToolCallIn); err != nil {
			slog.Error("ToolCallIn trigger failed", "error", err)
		}
	}
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

// copyResponse copies response headers and body from an *http.Response to
// an http.ResponseWriter.
func copyResponse(w http.ResponseWriter, resp *http.Response) {
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// writeCachedResponse writes a cached tool response back to the client.
// The cached map is expected to have "status_code" (float64 from JSON) and
// optional "headers" and "body" fields.
func writeCachedResponse(w http.ResponseWriter, cached map[string]any) {
	statusCode := http.StatusOK
	if sc, ok := cached["status_code"]; ok {
		switch v := sc.(type) {
		case int:
			statusCode = v
		case float64:
			statusCode = int(v)
		}
	}
	w.WriteHeader(statusCode)
	if body, ok := cached["body"].(string); ok {
		w.Write([]byte(body))
	}
}
