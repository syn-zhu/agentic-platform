package executor

import (
	"fmt"
	"sync"
)

// State represents the executor lifecycle state.
type State int

const (
	Idle     State = iota
	Starting
	Running
	Teardown
)

func (s State) String() string {
	switch s {
	case Idle:
		return "IDLE"
	case Starting:
		return "STARTING"
	case Running:
		return "RUNNING"
	case Teardown:
		return "TEARDOWN"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", int(s))
	}
}

// validTransitions defines allowed state transitions.
var validTransitions = map[State][]State{
	Idle:     {Starting},
	Starting: {Running, Teardown}, // Teardown on boot failure
	Running:  {Teardown},
	Teardown: {Idle},
}

// StateMachine enforces serial execution with valid state transitions.
type StateMachine struct {
	mu    sync.RWMutex
	state State
}

// NewStateMachine returns a state machine in the Idle state.
func NewStateMachine() *StateMachine {
	return &StateMachine{state: Idle}
}

// State returns the current state.
func (sm *StateMachine) State() State {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.state
}

// IsIdle returns true if the executor is ready for work.
func (sm *StateMachine) IsIdle() bool {
	return sm.State() == Idle
}

// Transition attempts to move to the target state.
// Returns an error if the transition is not allowed.
func (sm *StateMachine) Transition(target State) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	allowed := validTransitions[sm.state]
	for _, s := range allowed {
		if s == target {
			sm.state = target
			return nil
		}
	}
	return fmt.Errorf("invalid transition: %v → %v", sm.state, target)
}
