package proxy

import (
	"context"
	"fmt"
	"log/slog"
)

// Event represents a single execution event to be persisted.
type Event struct {
	SessionID   string
	ExecutionID string
	Type        string         // e.g., "execution_start", "http_request", "http_response", "execution_end"
	Data        map[string]any // event-specific payload

	// done is closed when the event has been persisted (or failed).
	// The caller blocks on this to "yield" execution until persistence
	// is confirmed, like a Temporal activity.
	done chan error
}

// EventLog serializes event persistence through a single channel.
// All producers (runner, proxy) send events to the channel. A single
// worker goroutine pulls events and writes them to the backing store.
// This guarantees:
//   - Strict ordering: events are persisted in the order they're sent
//   - Start/stop framing: execution_start is always first, execution_end always last
//   - No concurrent writes: one writer, no races
type EventLog struct {
	ch     chan *Event
	writer EventWriter
	cancel context.CancelFunc
}

// EventWriter is the backing persistence layer (e.g., MongoDB).
type EventWriter interface {
	WriteEvent(ctx context.Context, event *Event) error
}

// NewEventLog creates an EventLog with the given writer and starts
// the background worker.
func NewEventLog(writer EventWriter) *EventLog {
	ctx, cancel := context.WithCancel(context.Background())
	el := &EventLog{
		ch:     make(chan *Event, 64),
		writer: writer,
		cancel: cancel,
	}
	go el.worker(ctx)
	return el
}

// LogEvent sends an event to the log and blocks until it's persisted.
// This is the "yield" point — execution pauses until the event is written.
func (el *EventLog) LogEvent(ctx context.Context, sessionID, executionID, eventType string, data map[string]any) error {
	ev := &Event{
		SessionID:   sessionID,
		ExecutionID: executionID,
		Type:        eventType,
		Data:        data,
		done:        make(chan error, 1),
	}

	select {
	case el.ch <- ev:
	case <-ctx.Done():
		return ctx.Err()
	}

	select {
	case err := <-ev.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close drains the channel and stops the worker.
func (el *EventLog) Close() {
	el.cancel()
	close(el.ch)
}

// worker is the single goroutine that reads events and writes them.
func (el *EventLog) worker(ctx context.Context) {
	for ev := range el.ch {
		err := el.writer.WriteEvent(ctx, ev)
		if err != nil {
			slog.Error("event write failed",
				"session", ev.SessionID,
				"execution", ev.ExecutionID,
				"type", ev.Type,
				"error", err,
			)
		}
		ev.done <- err
	}
}

// NoopWriter is an EventWriter that does nothing. Used in tests
// or when event logging is disabled.
type NoopWriter struct{}

func (NoopWriter) WriteEvent(ctx context.Context, event *Event) error {
	return nil
}

// NewNoopEventLog creates an EventLog that discards events.
func NewNoopEventLog() *EventLog {
	return NewEventLog(NoopWriter{})
}

// LogEventAsync sends an event without waiting for persistence.
// Used when the caller doesn't need the "yield" guarantee
// (e.g., best-effort logging that shouldn't block execution).
func (el *EventLog) LogEventAsync(sessionID, executionID, eventType string, data map[string]any) {
	ev := &Event{
		SessionID:   sessionID,
		ExecutionID: executionID,
		Type:        eventType,
		Data:        data,
		done:        make(chan error, 1),
	}

	select {
	case el.ch <- ev:
		// Fire and forget — don't wait for done.
		go func() { <-ev.done }() // drain to prevent goroutine leak
	default:
		slog.Warn("event log channel full, dropping event",
			"type", eventType,
			"session", sessionID,
		)
	}
}

// Convenience event type constants.
const (
	EventExecutionStart = "execution_start"
	EventExecutionEnd   = "execution_end"
	EventHTTPRequest    = "http_request"
	EventHTTPResponse   = "http_response"
	EventError          = "error"
)

// FormatRequestData creates event data for an HTTP request.
func FormatRequestData(method, url string, headers map[string]string) map[string]any {
	return map[string]any{
		"method":  method,
		"url":     url,
		"headers": headers,
	}
}

// FormatResponseData creates event data for an HTTP response.
func FormatResponseData(statusCode int, headers map[string]string) map[string]any {
	return map[string]any{
		"status_code": statusCode,
		"headers":     headers,
	}
}

// FormatError creates a formatted error string for event data.
func FormatError(err error) map[string]any {
	return map[string]any{
		"error": fmt.Sprintf("%v", err),
	}
}
