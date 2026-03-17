package proxy_test

import (
	"context"
	"sync"
	"testing"

	"github.com/siyanzhu/agentic-platform/executor/internal/proxy"
)

func TestNoopEventLog(t *testing.T) {
	el := proxy.NewNoopEventLog()
	defer el.Close()

	err := el.LogEvent(context.Background(), "sess-1", "exec-1", "test", nil)
	if err != nil {
		t.Errorf("LogEvent: %v", err)
	}
}

func TestEventLogOrdering(t *testing.T) {
	var mu sync.Mutex
	var events []string

	writer := &recordingWriter{
		mu:     &mu,
		events: &events,
	}

	el := proxy.NewEventLog(writer)
	defer el.Close()

	ctx := context.Background()

	// Send events in order — the channel + single worker guarantees
	// they're persisted in the same order.
	el.LogEvent(ctx, "s", "e", "execution_start", nil)
	el.LogEvent(ctx, "s", "e", "http_request", nil)
	el.LogEvent(ctx, "s", "e", "http_response", nil)
	el.LogEvent(ctx, "s", "e", "http_request", nil)
	el.LogEvent(ctx, "s", "e", "http_response", nil)
	el.LogEvent(ctx, "s", "e", "execution_end", nil)

	mu.Lock()
	defer mu.Unlock()

	expected := []string{
		"execution_start",
		"http_request",
		"http_response",
		"http_request",
		"http_response",
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

func TestLogEventBlocksUntilPersisted(t *testing.T) {
	persisted := make(chan struct{})

	writer := &blockingWriter{persisted: persisted}
	el := proxy.NewEventLog(writer)
	defer el.Close()

	done := make(chan struct{})
	go func() {
		el.LogEvent(context.Background(), "s", "e", "test", nil)
		close(done)
	}()

	// LogEvent should block because the writer hasn't returned yet.
	select {
	case <-done:
		t.Fatal("LogEvent returned before writer finished")
	default:
	}

	// Unblock the writer.
	close(persisted)

	// Now LogEvent should complete.
	<-done
}

// recordingWriter records event types in order.
type recordingWriter struct {
	mu     *sync.Mutex
	events *[]string
}

func (w *recordingWriter) WriteEvent(ctx context.Context, event *proxy.Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	*w.events = append(*w.events, event.Type)
	return nil
}

// blockingWriter blocks until the persisted channel is closed.
type blockingWriter struct {
	persisted chan struct{}
}

func (w *blockingWriter) WriteEvent(ctx context.Context, event *proxy.Event) error {
	<-w.persisted
	return nil
}
