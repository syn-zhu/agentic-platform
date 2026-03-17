package proxy

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

// executionStep is an internal message sent through the serializer's channel.
type executionStep struct {
	event *ExecutionEvent
	done  chan error
}

// ExecutionSerializer ensures all execution events are persisted
// in strict order through a single worker goroutine. It owns:
//   - The channel + worker (serialization)
//   - The gate (execution_start must be persisted before any steps)
//   - The start/end framing (execution_start is first, execution_end is last)
//
// The proxy and runner call RecordExecutionStep(). The serializer
// guarantees ordering and blocks callers until persistence confirms
// ("yield" semantics, like Temporal activities).
type ExecutionSerializer struct {
	log    EventLog
	ch     chan *executionStep
	cancel context.CancelFunc

	// gate blocks RecordExecutionStep until StartExecution has persisted.
	mu       sync.Mutex
	gate     chan struct{}
	gateOnce sync.Once

	sessionID   string
	executionID string
}

// NewExecutionSerializer creates a serializer backed by the given EventLog.
func NewExecutionSerializer(log EventLog) *ExecutionSerializer {
	ctx, cancel := context.WithCancel(context.Background())
	s := &ExecutionSerializer{
		log:    log,
		ch:     make(chan *executionStep, 64),
		cancel: cancel,
		gate:   make(chan struct{}),
	}
	go s.worker(ctx)
	return s
}

// StartExecution records the execution_start event and opens the gate.
// Must be called before any RecordExecutionStep calls.
// Blocks until the start event is persisted.
func (s *ExecutionSerializer) StartExecution(ctx context.Context, sessionID, executionID string, data map[string]any) error {
	s.mu.Lock()
	s.sessionID = sessionID
	s.executionID = executionID
	s.gate = make(chan struct{})
	s.gateOnce = sync.Once{}
	s.mu.Unlock()

	// Write execution_start directly (bypasses the gate).
	err := s.writeAndWait(ctx, &ExecutionEvent{
		SessionID:   sessionID,
		ExecutionID: executionID,
		Type:        EventExecutionStart,
		Data:        data,
	})
	if err != nil {
		return err
	}

	// Open the gate — RecordExecutionStep calls can now proceed.
	s.gateOnce.Do(func() { close(s.gate) })
	return nil
}

// RecordExecutionStep records an event during execution.
// Blocks until the gate is open (execution_start persisted) and
// until this event is persisted ("yield" semantics).
func (s *ExecutionSerializer) RecordExecutionStep(ctx context.Context, eventType string, data map[string]any) error {
	// Get the gate channel under lock.
	s.mu.Lock()
	gate := s.gate
	s.mu.Unlock()

	// Wait for the gate (execution_start must be persisted first).
	select {
	case <-gate:
	case <-ctx.Done():
		return ctx.Err()
	}

	return s.writeAndWait(ctx, &ExecutionEvent{
		SessionID:   s.sessionID,
		ExecutionID: s.executionID,
		Type:        eventType,
		Data:        data,
	})
}

// EndExecution records the execution_end event with the final task state.
// Must be called after all RecordExecutionStep calls have returned.
// Blocks until the end event is persisted.
func (s *ExecutionSerializer) EndExecution(ctx context.Context, taskState string, result map[string]any) error {
	data := map[string]any{
		"task_state": taskState,
	}
	for k, v := range result {
		data[k] = v
	}

	return s.writeAndWait(ctx, &ExecutionEvent{
		SessionID:   s.sessionID,
		ExecutionID: s.executionID,
		Type:        EventExecutionEnd,
		Data:        data,
	})
}

// Close stops the worker.
func (s *ExecutionSerializer) Close() {
	s.cancel()
	close(s.ch)
}

// writeAndWait sends an event to the worker and blocks until persisted.
func (s *ExecutionSerializer) writeAndWait(ctx context.Context, event *ExecutionEvent) error {
	step := &executionStep{
		event: event,
		done:  make(chan error, 1),
	}

	select {
	case s.ch <- step:
	case <-ctx.Done():
		return ctx.Err()
	}

	select {
	case err := <-step.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// worker is the single goroutine that reads events and writes them.
func (s *ExecutionSerializer) worker(ctx context.Context) {
	for step := range s.ch {
		err := s.log.WriteEvent(ctx, step.event)
		if err != nil {
			slog.Error("event write failed",
				"session", step.event.SessionID,
				"execution", step.event.ExecutionID,
				"type", step.event.Type,
				"error", err,
			)
		}
		step.done <- err
	}
}

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

// FormatError creates event data for an error.
func FormatError(err error) map[string]any {
	return map[string]any{
		"error": fmt.Sprintf("%v", err),
	}
}
