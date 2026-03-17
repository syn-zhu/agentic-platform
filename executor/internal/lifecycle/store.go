package lifecycle

import "context"

// EventStore is the persistence interface for execution state and events.
type EventStore interface {
	SaveState(ctx context.Context, execID string, state ExecState, args ...any) error
	LoadState(ctx context.Context, execID string) (ExecState, []any, error)
	AppendEvent(ctx context.Context, execID string, eventType string, data map[string]any) error
	LoadPreviousExecution(ctx context.Context, sessionID string) (*PreviousExecution, error)
}

type PreviousExecution struct {
	ExecID string
	State  ExecState
	Args   []any
	Events []Event
}

type Event struct {
	Type string
	Data map[string]any
}

type NoopEventStore struct{}

func (NoopEventStore) SaveState(_ context.Context, _ string, _ ExecState, _ ...any) error {
	return nil
}
func (NoopEventStore) LoadState(_ context.Context, _ string) (ExecState, []any, error) {
	return ExecPending, nil, nil
}
func (NoopEventStore) AppendEvent(_ context.Context, _ string, _ string, _ map[string]any) error {
	return nil
}
func (NoopEventStore) LoadPreviousExecution(_ context.Context, _ string) (*PreviousExecution, error) {
	return nil, nil
}
