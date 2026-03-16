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

	// IDLE → STARTING → RUNNING → WARM → RUNNING (resume) → WARM → TEARDOWN → IDLE
	for _, target := range []executor.State{
		executor.Starting, executor.Running, executor.Warm,
		executor.Running, executor.Warm,
		executor.Teardown, executor.Idle,
	} {
		if err := sm.Transition(target); err != nil {
			t.Fatalf("%v → %v: %v", sm.State(), target, err)
		}
	}
}

func TestStartingToTeardown(t *testing.T) {
	sm := executor.NewStateMachine()
	_ = sm.Transition(executor.Starting)

	if err := sm.Transition(executor.Teardown); err != nil {
		t.Fatalf("STARTING→TEARDOWN: %v", err)
	}
}

func TestRunningToWarm(t *testing.T) {
	sm := executor.NewStateMachine()
	_ = sm.Transition(executor.Starting)
	_ = sm.Transition(executor.Running)

	if err := sm.Transition(executor.Warm); err != nil {
		t.Fatalf("RUNNING→WARM: %v", err)
	}
}

func TestWarmToRunning(t *testing.T) {
	sm := executor.NewStateMachine()
	_ = sm.Transition(executor.Starting)
	_ = sm.Transition(executor.Running)
	_ = sm.Transition(executor.Warm)

	if err := sm.Transition(executor.Running); err != nil {
		t.Fatalf("WARM→RUNNING: %v", err)
	}
}

func TestWarmToTeardown(t *testing.T) {
	sm := executor.NewStateMachine()
	_ = sm.Transition(executor.Starting)
	_ = sm.Transition(executor.Running)
	_ = sm.Transition(executor.Warm)

	if err := sm.Transition(executor.Teardown); err != nil {
		t.Fatalf("WARM→TEARDOWN: %v", err)
	}
}

func TestInvalidTransition(t *testing.T) {
	sm := executor.NewStateMachine()

	if err := sm.Transition(executor.Running); err == nil {
		t.Fatal("IDLE→RUNNING should fail")
	}
	if err := sm.Transition(executor.Warm); err == nil {
		t.Fatal("IDLE→WARM should fail")
	}
	if err := sm.Transition(executor.Teardown); err == nil {
		t.Fatal("IDLE→TEARDOWN should fail")
	}
}

func TestIsAvailable(t *testing.T) {
	sm := executor.NewStateMachine()

	// IDLE is available.
	if !sm.IsAvailable() {
		t.Fatal("IDLE should be available")
	}

	// STARTING is not available.
	_ = sm.Transition(executor.Starting)
	if sm.IsAvailable() {
		t.Fatal("STARTING should not be available")
	}

	// RUNNING is not available.
	_ = sm.Transition(executor.Running)
	if sm.IsAvailable() {
		t.Fatal("RUNNING should not be available")
	}

	// WARM is available.
	_ = sm.Transition(executor.Warm)
	if !sm.IsAvailable() {
		t.Fatal("WARM should be available")
	}
}

func TestIsWarm(t *testing.T) {
	sm := executor.NewStateMachine()
	if sm.IsWarm() {
		t.Fatal("new state machine should not be warm")
	}
	_ = sm.Transition(executor.Starting)
	_ = sm.Transition(executor.Running)
	_ = sm.Transition(executor.Warm)
	if !sm.IsWarm() {
		t.Fatal("should be warm after RUNNING→WARM")
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
		{executor.Warm, "WARM"},
		{executor.Teardown, "TEARDOWN"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("State(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}
