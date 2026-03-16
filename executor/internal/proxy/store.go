package proxy

import (
	"context"
	"net/http"
)

// EventStore persists outbound request/response event logs and
// provides a replay cache for idempotent resume.
type EventStore interface {
	// LogRequest records an outbound request.
	LogRequest(ctx context.Context, sessionID string, step int, req *http.Request) error

	// LogResponse records the response to an outbound request.
	LogResponse(ctx context.Context, sessionID string, step int, resp *http.Response) error

	// GetCachedResponse returns a cached response for replay.
	// Returns nil, nil if no cached entry exists.
	GetCachedResponse(ctx context.Context, sessionID string, step int) (*http.Response, error)
}

// NoopStore is an EventStore that does nothing. Used when event
// logging is disabled or during tests.
type NoopStore struct{}

func (NoopStore) LogRequest(ctx context.Context, sessionID string, step int, req *http.Request) error {
	return nil
}

func (NoopStore) LogResponse(ctx context.Context, sessionID string, step int, resp *http.Response) error {
	return nil
}

func (NoopStore) GetCachedResponse(ctx context.Context, sessionID string, step int) (*http.Response, error) {
	return nil, nil
}
