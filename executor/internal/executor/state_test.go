package executor_test

import (
	"testing"

	"github.com/siyanzhu/agentic-platform/executor/internal/executor"
)

func TestStateTransitions(t *testing.T) {
	sm := executor.NewStateMachine()

	if sm.State() != executor.Idle {
		t.Fatalf("initial state = %v, want Idle", sm.State())
	}

	// IDLE → STARTING
	if err := sm.Transition(executor.Starting); err != nil {
		t.Fatalf("IDLE→STARTING: %v", err)
	}

	// STARTING → RUNNING
	if err := sm.Transition(executor.Running); err != nil {
		t.Fatalf("STARTING→RUNNING: %v", err)
	}

	// RUNNING → TEARDOWN
	if err := sm.Transition(executor.Teardown); err != nil {
		t.Fatalf("RUNNING→TEARDOWN: %v", err)
	}

	// TEARDOWN → IDLE
	if err := sm.Transition(executor.Idle); err != nil {
		t.Fatalf("TEARDOWN→IDLE: %v", err)
	}
}

func TestStartingToTeardown(t *testing.T) {
	sm := executor.NewStateMachine()
	_ = sm.Transition(executor.Starting)

	// STARTING → TEARDOWN (boot failure path)
	if err := sm.Transition(executor.Teardown); err != nil {
		t.Fatalf("STARTING→TEARDOWN: %v", err)
	}
}

func TestInvalidTransition(t *testing.T) {
	sm := executor.NewStateMachine()

	// IDLE → RUNNING (skip STARTING)
	if err := sm.Transition(executor.Running); err == nil {
		t.Fatal("IDLE→RUNNING should fail")
	}

	// IDLE → TEARDOWN
	if err := sm.Transition(executor.Teardown); err == nil {
		t.Fatal("IDLE→TEARDOWN should fail")
	}
}

func TestConcurrentTransitionRejection(t *testing.T) {
	sm := executor.NewStateMachine()
	_ = sm.Transition(executor.Starting)

	if err := sm.Transition(executor.Starting); err == nil {
		t.Fatal("STARTING→STARTING should fail")
	}
}

func TestIsIdle(t *testing.T) {
	sm := executor.NewStateMachine()
	if !sm.IsIdle() {
		t.Fatal("new state machine should be idle")
	}
	_ = sm.Transition(executor.Starting)
	if sm.IsIdle() {
		t.Fatal("should not be idle after transitioning to STARTING")
	}
}

func TestStateString(t *testing.T) {
	tests := []struct {
		state executor.State
		want  string
	}{
		{executor.Idle, "IDLE"},
		{executor.Starting, "STARTING"},
		{executor.Running, "RUNNING"},
		{executor.Teardown, "TEARDOWN"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("State(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}
