package proxy_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/siyanzhu/agentic-platform/executor/internal/proxy"
)

func TestNoopStore(t *testing.T) {
	store := proxy.NoopStore{}
	ctx := context.Background()

	if err := store.LogRequest(ctx, "sess-1", 1, nil); err != nil {
		t.Errorf("LogRequest: %v", err)
	}
	if err := store.LogResponse(ctx, "sess-1", 1, nil); err != nil {
		t.Errorf("LogResponse: %v", err)
	}

	resp, err := store.GetCachedResponse(ctx, "sess-1", 1)
	if err != nil {
		t.Errorf("GetCachedResponse: %v", err)
	}
	if resp != nil {
		t.Error("expected nil response from NoopStore")
	}
}

func TestEventStoreInterface(t *testing.T) {
	// Verify NoopStore implements EventStore.
	var _ proxy.EventStore = proxy.NoopStore{}
	var _ proxy.EventStore = &proxy.NoopStore{}
}

// MemoryStore implements EventStore for testing.
type MemoryStore struct {
	requests  map[string]map[int]*http.Request
	responses map[string]map[int]*http.Response
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		requests:  make(map[string]map[int]*http.Request),
		responses: make(map[string]map[int]*http.Response),
	}
}

func (m *MemoryStore) LogRequest(ctx context.Context, sessionID string, step int, req *http.Request) error {
	if m.requests[sessionID] == nil {
		m.requests[sessionID] = make(map[int]*http.Request)
	}
	m.requests[sessionID][step] = req
	return nil
}

func (m *MemoryStore) LogResponse(ctx context.Context, sessionID string, step int, resp *http.Response) error {
	if m.responses[sessionID] == nil {
		m.responses[sessionID] = make(map[int]*http.Response)
	}
	m.responses[sessionID][step] = resp
	return nil
}

func (m *MemoryStore) GetCachedResponse(ctx context.Context, sessionID string, step int) (*http.Response, error) {
	if m.responses[sessionID] == nil {
		return nil, nil
	}
	return m.responses[sessionID][step], nil
}

func TestMemoryStoreImplementsEventStore(t *testing.T) {
	var _ proxy.EventStore = &MemoryStore{}
}
