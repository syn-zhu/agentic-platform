package proxy

import (
	"context"
)

// ExecutionEvent represents a single recorded event in the execution DAG.
type ExecutionEvent struct {
	SessionID   string
	ExecutionID string
	Type        string         // e.g., "execution_start", "http_request", "execution_end"
	Data        map[string]any // event-specific payload
}

// EventLog is the backing persistence layer for execution events.
// Implementations write events to a durable store (e.g., MongoDB).
// The EventLog does NOT handle ordering or synchronization —
// that's the ExecutionSerializer's job.
type EventLog interface {
	WriteEvent(ctx context.Context, event *ExecutionEvent) error
}

// NoopEventLog discards all events. Used in tests or when
// event logging is disabled.
type NoopEventLog struct{}

func (NoopEventLog) WriteEvent(ctx context.Context, event *ExecutionEvent) error {
	return nil
}

// Event type constants.
const (
	EventExecutionStart = "execution_start"
	EventExecutionEnd   = "execution_end"
	EventToolRequest    = "tool_request"
	EventToolResponse   = "tool_response"
	EventError          = "error"
)
