package lifecycle_test

import (
	"context"
	"testing"

	"github.com/siyanzhu/agentic-platform/executor/internal/lifecycle"
)

// inMemoryStore is a simple in-memory EventStore for testing.
type inMemoryStore struct {
	state  lifecycle.ExecState
	args   []any
	events []lifecycle.Event
	prevExec *lifecycle.PreviousExecution
}

func newInMemoryStore() *inMemoryStore {
	return &inMemoryStore{
		state: lifecycle.ExecPending,
	}
}

func (s *inMemoryStore) SaveState(_ context.Context, _ string, state lifecycle.ExecState, args ...any) error {
	s.state = state
	s.args = args
	return nil
}

func (s *inMemoryStore) LoadState(_ context.Context, _ string) (lifecycle.ExecState, []any, error) {
	return s.state, s.args, nil
}

func (s *inMemoryStore) AppendEvent(_ context.Context, _ string, eventType string, data map[string]any) error {
	s.events = append(s.events, lifecycle.Event{Type: eventType, Data: data})
	return nil
}

func (s *inMemoryStore) LoadPreviousExecution(_ context.Context, _ string) (*lifecycle.PreviousExecution, error) {
	return s.prevExec, nil
}

func newTestLifecycle(store lifecycle.EventStore) *lifecycle.ExecutionLifecycle {
	_, cancel := context.WithCancel(context.Background())
	return lifecycle.NewExecutionLifecycle(store, "exec-1", "session-1", cancel)
}

func mustState(t *testing.T, el *lifecycle.ExecutionLifecycle) lifecycle.ExecState {
	t.Helper()
	st, err := el.SM.State(context.Background())
	if err != nil {
		t.Fatalf("failed to get state: %v", err)
	}
	return st.(lifecycle.ExecState)
}

func TestInitialStateIsPending(t *testing.T) {
	store := newInMemoryStore()
	el := newTestLifecycle(store)
	if got := mustState(t, el); got != lifecycle.ExecPending {
		t.Fatalf("expected Pending, got %s", got)
	}
}

func TestPendingToActiveOnRunReceived(t *testing.T) {
	store := newInMemoryStore()
	el := newTestLifecycle(store)

	if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigRunReceived); err != nil {
		t.Fatalf("RunReceived failed: %v", err)
	}
	if got := mustState(t, el); got != lifecycle.ExecActive {
		t.Fatalf("expected Active, got %s", got)
	}
}

func TestActiveToCompletedOnComplete(t *testing.T) {
	store := newInMemoryStore()
	el := newTestLifecycle(store)

	if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigRunReceived); err != nil {
		t.Fatalf("RunReceived failed: %v", err)
	}
	if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigComplete); err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if got := mustState(t, el); got != lifecycle.ExecCompleted {
		t.Fatalf("expected Completed, got %s", got)
	}
}

func TestActiveToFailedOnFail(t *testing.T) {
	store := newInMemoryStore()
	el := newTestLifecycle(store)

	if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigRunReceived); err != nil {
		t.Fatalf("RunReceived failed: %v", err)
	}
	if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigFail); err != nil {
		t.Fatalf("Fail failed: %v", err)
	}
	if got := mustState(t, el); got != lifecycle.ExecFailed {
		t.Fatalf("expected Failed, got %s", got)
	}
}

func TestCompleteBlockedByInflightOps(t *testing.T) {
	store := newInMemoryStore()
	el := newTestLifecycle(store)

	if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigRunReceived); err != nil {
		t.Fatalf("RunReceived failed: %v", err)
	}

	// Fire a tool call out — inflightOps should increase.
	if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigToolCallOut); err != nil {
		t.Fatalf("ToolCallOut failed: %v", err)
	}
	if el.InflightOps() != 1 {
		t.Fatalf("expected inflightOps=1, got %d", el.InflightOps())
	}

	// Complete should fail because guard blocks it.
	if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigComplete); err == nil {
		t.Fatal("expected Complete to fail with inflight ops, but it succeeded")
	}

	// State should still be Active.
	if got := mustState(t, el); got != lifecycle.ExecActive {
		t.Fatalf("expected Active, got %s", got)
	}

	// Resolve the tool call.
	if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigToolCallIn); err != nil {
		t.Fatalf("ToolCallIn failed: %v", err)
	}
	if el.InflightOps() != 0 {
		t.Fatalf("expected inflightOps=0, got %d", el.InflightOps())
	}

	// Now Complete should succeed.
	if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigComplete); err != nil {
		t.Fatalf("Complete failed after resolving ops: %v", err)
	}
	if got := mustState(t, el); got != lifecycle.ExecCompleted {
		t.Fatalf("expected Completed, got %s", got)
	}
}

func TestCancelBypassesGuard(t *testing.T) {
	store := newInMemoryStore()
	el := newTestLifecycle(store)

	if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigRunReceived); err != nil {
		t.Fatalf("RunReceived failed: %v", err)
	}

	// Fire a tool call out to create inflight ops.
	if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigToolCallOut); err != nil {
		t.Fatalf("ToolCallOut failed: %v", err)
	}
	if el.InflightOps() != 1 {
		t.Fatalf("expected inflightOps=1, got %d", el.InflightOps())
	}

	// Cancel should succeed despite inflight ops.
	if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigCancel); err != nil {
		t.Fatalf("Cancel failed: %v", err)
	}
	if got := mustState(t, el); got != lifecycle.ExecCancelled {
		t.Fatalf("expected Cancelled, got %s", got)
	}
	// inflightOps should be reset to 0 by OnEntry for Cancelled.
	if el.InflightOps() != 0 {
		t.Fatalf("expected inflightOps=0 after cancel, got %d", el.InflightOps())
	}
}

func TestVMCrashBypassesGuard(t *testing.T) {
	store := newInMemoryStore()
	el := newTestLifecycle(store)

	if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigRunReceived); err != nil {
		t.Fatalf("RunReceived failed: %v", err)
	}

	// Fire a tool call out.
	if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigToolCallOut); err != nil {
		t.Fatalf("ToolCallOut failed: %v", err)
	}

	// VMCrash should succeed despite inflight ops.
	if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigVMCrash); err != nil {
		t.Fatalf("VMCrash failed: %v", err)
	}
	if got := mustState(t, el); got != lifecycle.ExecFailed {
		t.Fatalf("expected Failed, got %s", got)
	}
}

func TestToolCallInternalTransitionsDontChangeState(t *testing.T) {
	store := newInMemoryStore()
	el := newTestLifecycle(store)

	if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigRunReceived); err != nil {
		t.Fatalf("RunReceived failed: %v", err)
	}

	// ToolCallOut should not change state.
	if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigToolCallOut); err != nil {
		t.Fatalf("ToolCallOut failed: %v", err)
	}
	if got := mustState(t, el); got != lifecycle.ExecActive {
		t.Fatalf("expected Active after ToolCallOut, got %s", got)
	}

	// ToolCallIn should not change state.
	if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigToolCallIn); err != nil {
		t.Fatalf("ToolCallIn failed: %v", err)
	}
	if got := mustState(t, el); got != lifecycle.ExecActive {
		t.Fatalf("expected Active after ToolCallIn, got %s", got)
	}

	// ToolCallError should not change state (send another out first).
	if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigToolCallOut); err != nil {
		t.Fatalf("ToolCallOut failed: %v", err)
	}
	if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigToolCallError); err != nil {
		t.Fatalf("ToolCallError failed: %v", err)
	}
	if got := mustState(t, el); got != lifecycle.ExecActive {
		t.Fatalf("expected Active after ToolCallError, got %s", got)
	}
}

func TestReentrantInternalTransitions(t *testing.T) {
	store := newInMemoryStore()
	el := newTestLifecycle(store)

	if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigRunReceived); err != nil {
		t.Fatalf("RunReceived failed: %v", err)
	}

	// ReentrantIn should increment inflightOps and keep Active state.
	if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigReentrantIn); err != nil {
		t.Fatalf("ReentrantIn failed: %v", err)
	}
	if got := mustState(t, el); got != lifecycle.ExecActive {
		t.Fatalf("expected Active after ReentrantIn, got %s", got)
	}
	if el.InflightOps() != 1 {
		t.Fatalf("expected inflightOps=1, got %d", el.InflightOps())
	}

	// Complete should be blocked.
	if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigComplete); err == nil {
		t.Fatal("expected Complete to fail with inflight reentrant, but it succeeded")
	}

	// ReentrantOut should decrement.
	if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigReentrantOut); err != nil {
		t.Fatalf("ReentrantOut failed: %v", err)
	}
	if el.InflightOps() != 0 {
		t.Fatalf("expected inflightOps=0, got %d", el.InflightOps())
	}

	// Now Complete should succeed.
	if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigComplete); err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if got := mustState(t, el); got != lifecycle.ExecCompleted {
		t.Fatalf("expected Completed, got %s", got)
	}
}

func TestCannotFireToolCallOutFromPending(t *testing.T) {
	store := newInMemoryStore()
	el := newTestLifecycle(store)

	err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigToolCallOut)
	if err == nil {
		t.Fatal("expected error firing ToolCallOut from Pending, but got nil")
	}
}

func TestCannotTransitionFromTerminalStates(t *testing.T) {
	tests := []struct {
		name         string
		terminalFire lifecycle.ExecTrigger
		terminalState lifecycle.ExecState
	}{
		{"Completed", lifecycle.ExecTrigComplete, lifecycle.ExecCompleted},
		{"Failed", lifecycle.ExecTrigFail, lifecycle.ExecFailed},
		{"Cancelled", lifecycle.ExecTrigCancel, lifecycle.ExecCancelled},
	}

	triggers := []lifecycle.ExecTrigger{
		lifecycle.ExecTrigRunReceived,
		lifecycle.ExecTrigToolCallOut,
		lifecycle.ExecTrigComplete,
		lifecycle.ExecTrigFail,
		lifecycle.ExecTrigCancel,
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newInMemoryStore()
			el := newTestLifecycle(store)

			// Get to Active.
			if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigRunReceived); err != nil {
				t.Fatalf("RunReceived failed: %v", err)
			}

			// Transition to terminal state.
			if err := el.SM.FireCtx(context.Background(), tc.terminalFire); err != nil {
				t.Fatalf("%s failed: %v", tc.terminalFire, err)
			}
			if got := mustState(t, el); got != tc.terminalState {
				t.Fatalf("expected %s, got %s", tc.terminalState, got)
			}

			// Try all triggers — all should fail.
			for _, trig := range triggers {
				err := el.SM.FireCtx(context.Background(), trig)
				if err == nil {
					t.Errorf("expected error firing %s from %s, but got nil", trig, tc.terminalState)
				}
			}
		})
	}
}

func TestInputRequiredTransition(t *testing.T) {
	store := newInMemoryStore()
	el := newTestLifecycle(store)

	if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigRunReceived); err != nil {
		t.Fatalf("RunReceived failed: %v", err)
	}
	if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigInputRequired); err != nil {
		t.Fatalf("InputRequired failed: %v", err)
	}
	if got := mustState(t, el); got != lifecycle.ExecInputRequired {
		t.Fatalf("expected InputRequired, got %s", got)
	}
}

func TestInputRequiredBlockedByInflightOps(t *testing.T) {
	store := newInMemoryStore()
	el := newTestLifecycle(store)

	if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigRunReceived); err != nil {
		t.Fatalf("RunReceived failed: %v", err)
	}
	if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigToolCallOut); err != nil {
		t.Fatalf("ToolCallOut failed: %v", err)
	}

	err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigInputRequired)
	if err == nil {
		t.Fatal("expected InputRequired to fail with inflight ops, but it succeeded")
	}

	// Resolve and retry.
	if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigToolCallIn); err != nil {
		t.Fatalf("ToolCallIn failed: %v", err)
	}
	if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigInputRequired); err != nil {
		t.Fatalf("InputRequired failed after resolving: %v", err)
	}
	if got := mustState(t, el); got != lifecycle.ExecInputRequired {
		t.Fatalf("expected InputRequired, got %s", got)
	}
}

func TestBuildToolCacheWithPreviousExecution(t *testing.T) {
	store := newInMemoryStore()
	store.prevExec = &lifecycle.PreviousExecution{
		ExecID: "prev-exec",
		State:  lifecycle.ExecCompleted,
		Events: []lifecycle.Event{
			{Type: string(lifecycle.ExecTrigToolCallOut), Data: map[string]any{"url": "http://tool-a/run"}},
			{Type: string(lifecycle.ExecTrigToolCallOut), Data: map[string]any{"url": "http://tool-b/run"}},
			{Type: string(lifecycle.ExecTrigToolCallIn), Data: map[string]any{"url": "http://tool-a/run", "result": "res-a"}},
			{Type: string(lifecycle.ExecTrigToolCallIn), Data: map[string]any{"url": "http://tool-b/run", "result": "res-b"}},
		},
	}

	_, cancel := context.WithCancel(context.Background())
	el := lifecycle.NewExecutionLifecycle(store, "exec-2", "session-1", cancel)

	// Transition to Active to trigger OnEntry which loads cache.
	if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigRunReceived); err != nil {
		t.Fatalf("RunReceived failed: %v", err)
	}

	if el.CachedToolResults == nil {
		t.Fatal("expected CachedToolResults to be populated, got nil")
	}
	if len(el.CachedToolResults) != 2 {
		t.Fatalf("expected 2 cached entries, got %d", len(el.CachedToolResults))
	}
	resA, ok := el.CachedToolResults["http://tool-a/run"]
	if !ok {
		t.Fatal("expected cache entry for http://tool-a/run")
	}
	if resA["result"] != "res-a" {
		t.Fatalf("expected result res-a, got %v", resA["result"])
	}
}

func TestDecrementGuardsAgainstNegative(t *testing.T) {
	store := newInMemoryStore()
	el := newTestLifecycle(store)

	if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigRunReceived); err != nil {
		t.Fatalf("RunReceived failed: %v", err)
	}

	// Fire ToolCallIn without a preceding ToolCallOut — inflightOps should not go negative.
	if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigToolCallIn); err != nil {
		t.Fatalf("ToolCallIn failed: %v", err)
	}
	if el.InflightOps() != 0 {
		t.Fatalf("expected inflightOps=0 (guarded against negative), got %d", el.InflightOps())
	}
}

func TestFailBlockedByInflightOps(t *testing.T) {
	store := newInMemoryStore()
	el := newTestLifecycle(store)

	if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigRunReceived); err != nil {
		t.Fatalf("RunReceived failed: %v", err)
	}
	if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigToolCallOut); err != nil {
		t.Fatalf("ToolCallOut failed: %v", err)
	}

	// Fail should be blocked.
	err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigFail)
	if err == nil {
		t.Fatal("expected Fail to be blocked with inflight ops, but it succeeded")
	}

	// Resolve and retry.
	if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigToolCallIn); err != nil {
		t.Fatalf("ToolCallIn failed: %v", err)
	}
	if err := el.SM.FireCtx(context.Background(), lifecycle.ExecTrigFail); err != nil {
		t.Fatalf("Fail failed after resolving: %v", err)
	}
	if got := mustState(t, el); got != lifecycle.ExecFailed {
		t.Fatalf("expected Failed, got %s", got)
	}
}
