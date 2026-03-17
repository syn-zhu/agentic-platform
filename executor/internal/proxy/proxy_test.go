package proxy_test

import (
	"context"
	"sync"
	"testing"

	"github.com/siyanzhu/agentic-platform/executor/internal/proxy"
)

func TestNoopEventLog(t *testing.T) {
	var log proxy.EventLog = proxy.NoopEventLog{}
	err := log.WriteEvent(context.Background(), &proxy.ExecutionEvent{
		SessionID: "s", ExecutionID: "e", Type: "test",
	})
	if err != nil {
		t.Errorf("WriteEvent: %v", err)
	}
}

func TestSerializerOrdering(t *testing.T) {
	var mu sync.Mutex
	var events []string

	writer := &recordingWriter{mu: &mu, events: &events}
	s := proxy.NewExecutionSerializer(writer)
	defer s.Close()

	ctx := context.Background()

	s.StartExecution(ctx, "s", "e", nil)
	s.RecordExecutionStep(ctx, "tool_request", nil)
	s.RecordExecutionStep(ctx, "tool_response", nil)
	s.RecordExecutionStep(ctx, "tool_request", nil)
	s.RecordExecutionStep(ctx, "tool_response", nil)
	s.EndExecution(ctx, "COMPLETED", nil)

	mu.Lock()
	defer mu.Unlock()

	expected := []string{
		"execution_start",
		"tool_request",
		"tool_response",
		"tool_request",
		"tool_response",
		"execution_end",
	}

	if len(events) != len(expected) {
		t.Fatalf("got %d events, want %d", len(events), len(expected))
	}
	for i, e := range events {
		if e != expected[i] {
			t.Errorf("event[%d] = %q, want %q", i, e, expected[i])
		}
	}
}

func TestSerializerBlocksUntilPersisted(t *testing.T) {
	persisted := make(chan struct{})
	writer := &blockingWriter{persisted: persisted}
	s := proxy.NewExecutionSerializer(writer)
	defer s.Close()

	done := make(chan struct{})
	go func() {
		s.StartExecution(context.Background(), "s", "e", nil)
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("StartExecution returned before writer finished")
	default:
	}

	close(persisted)
	<-done
}

func TestRecordStepBlocksUntilGateOpens(t *testing.T) {
	writer := &recordingWriter{mu: &sync.Mutex{}, events: &[]string{}}
	s := proxy.NewExecutionSerializer(writer)
	defer s.Close()

	stepped := make(chan struct{})
	go func() {
		// This should block because StartExecution hasn't been called.
		s.RecordExecutionStep(context.Background(), "test", nil)
		close(stepped)
	}()

	select {
	case <-stepped:
		t.Fatal("RecordExecutionStep should block before StartExecution")
	default:
	}

	// Now start execution — this opens the gate.
	s.StartExecution(context.Background(), "s", "e", nil)
	<-stepped // RecordExecutionStep should now complete.
}

// recordingWriter records event types in order.
type recordingWriter struct {
	mu     *sync.Mutex
	events *[]string
}

func (w *recordingWriter) WriteEvent(ctx context.Context, event *proxy.ExecutionEvent) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	*w.events = append(*w.events, event.Type)
	return nil
}

// blockingWriter blocks until the persisted channel is closed.
type blockingWriter struct {
	persisted chan struct{}
}

func (w *blockingWriter) WriteEvent(ctx context.Context, event *proxy.ExecutionEvent) error {
	<-w.persisted
	return nil
}
