package lifecycle

import (
	"context"

	"github.com/qmuntal/stateless"
)

// ExecutionLifecycle wraps a stateless.StateMachine to manage the lifecycle
// of a single /run invocation through Pending → Active → terminal states.
type ExecutionLifecycle struct {
	SM                *stateless.StateMachine
	store             EventStore
	execID            string
	sessionID         string
	inflightOps       int
	cancelFunc        context.CancelFunc
	CachedToolResults map[string]map[string]any
}

// NewExecutionLifecycle creates a new execution lifecycle state machine backed
// by the given EventStore. The cancel function is called when the execution
// enters a terminal failure or cancellation state.
func NewExecutionLifecycle(store EventStore, execID, sessionID string, cancel context.CancelFunc) *ExecutionLifecycle {
	el := &ExecutionLifecycle{
		store:      store,
		execID:     execID,
		sessionID:  sessionID,
		cancelFunc: cancel,
	}

	accessor := func(ctx context.Context) (stateless.State, []any, error) {
		state, args, err := store.LoadState(ctx, execID)
		if err != nil {
			return nil, nil, err
		}
		return state, args, nil
	}

	mutator := func(ctx context.Context, state stateless.State, args ...any) error {
		return store.SaveState(ctx, execID, state.(ExecState), args...)
	}

	el.SM = stateless.NewStateMachineWithExternalStorageAndArgs(accessor, mutator, stateless.FiringQueued)
	el.configure()
	return el
}

// InflightOps returns the number of currently in-flight outbound tool calls
// and re-entrant calls.
func (el *ExecutionLifecycle) InflightOps() int {
	return el.inflightOps
}

// allOpsResolved is a guard that returns true only when there are no in-flight
// operations. It gates transitions to Complete, Fail, and InputRequired.
func (el *ExecutionLifecycle) allOpsResolved(_ context.Context, _ ...any) bool {
	return el.inflightOps == 0
}

func (el *ExecutionLifecycle) configure() {
	// --- Pending ---
	el.SM.Configure(ExecPending).
		Permit(ExecTrigRunReceived, ExecActive)

	// --- Active ---
	active := el.SM.Configure(ExecActive)

	// Guarded terminal transitions.
	active.Permit(ExecTrigComplete, ExecCompleted, el.allOpsResolved)
	active.Permit(ExecTrigFail, ExecFailed, el.allOpsResolved)
	active.Permit(ExecTrigInputRequired, ExecInputRequired, el.allOpsResolved)

	// Unguarded terminal transitions — Cancel and VMCrash bypass the guard.
	active.Permit(ExecTrigCancel, ExecCancelled)
	active.Permit(ExecTrigVMCrash, ExecFailed)

	// Tool call internal transitions (don't change state).
	active.InternalTransition(ExecTrigToolCallOut, func(ctx context.Context, args ...any) error {
		el.inflightOps++
		return el.store.AppendEvent(ctx, el.execID, string(ExecTrigToolCallOut), nil)
	})

	active.InternalTransition(ExecTrigToolCallIn, func(ctx context.Context, args ...any) error {
		if el.inflightOps > 0 {
			el.inflightOps--
		}
		return el.store.AppendEvent(ctx, el.execID, string(ExecTrigToolCallIn), nil)
	})

	active.InternalTransition(ExecTrigToolCallError, func(ctx context.Context, args ...any) error {
		if el.inflightOps > 0 {
			el.inflightOps--
		}
		return el.store.AppendEvent(ctx, el.execID, string(ExecTrigToolCallError), nil)
	})

	// Re-entrant call internal transitions.
	active.InternalTransition(ExecTrigReentrantIn, func(ctx context.Context, args ...any) error {
		el.inflightOps++
		return el.store.AppendEvent(ctx, el.execID, string(ExecTrigReentrantIn), nil)
	})

	active.InternalTransition(ExecTrigReentrantOut, func(ctx context.Context, args ...any) error {
		if el.inflightOps > 0 {
			el.inflightOps--
		}
		return el.store.AppendEvent(ctx, el.execID, string(ExecTrigReentrantOut), nil)
	})

	// OnEntry for Active: load previous execution's tool results for caching.
	active.OnEntry(func(ctx context.Context, args ...any) error {
		prev, err := el.store.LoadPreviousExecution(ctx, el.sessionID)
		if err != nil {
			return err
		}
		if prev != nil && len(prev.Events) > 0 {
			el.CachedToolResults = buildToolCache(prev.Events)
		}
		return nil
	})

	// --- Completed (terminal) ---
	el.SM.Configure(ExecCompleted)

	// --- Failed (terminal) ---
	el.SM.Configure(ExecFailed).
		OnEntry(func(_ context.Context, _ ...any) error {
			el.inflightOps = 0
			el.cancelFunc()
			return nil
		})

	// --- InputRequired (terminal) ---
	el.SM.Configure(ExecInputRequired)

	// --- Cancelled (terminal) ---
	el.SM.Configure(ExecCancelled).
		OnEntry(func(_ context.Context, _ ...any) error {
			el.inflightOps = 0
			el.cancelFunc()
			return nil
		})
}

// buildToolCache creates a URL→response map from previous execution events.
// It uses a FIFO queue of pending URLs to correctly match ToolCallOut events
// with their corresponding ToolCallIn responses, even for concurrent tool calls.
func buildToolCache(events []Event) map[string]map[string]any {
	cache := make(map[string]map[string]any)
	// FIFO queue of pending URLs from ToolCallOut events.
	var pendingURLs []string

	for _, evt := range events {
		switch evt.Type {
		case string(ExecTrigToolCallOut):
			if evt.Data != nil {
				if url, ok := evt.Data["url"].(string); ok {
					pendingURLs = append(pendingURLs, url)
				}
			}
		case string(ExecTrigToolCallIn):
			if evt.Data != nil {
				// Try to match by URL in the response data first.
				if url, ok := evt.Data["url"].(string); ok {
					cache[url] = evt.Data
				} else if len(pendingURLs) > 0 {
					// Fall back to FIFO queue matching.
					url = pendingURLs[0]
					pendingURLs = pendingURLs[1:]
					cache[url] = evt.Data
				}
			}
		}
	}

	return cache
}
