package executor

import (
	"fmt"
	"sync"
)

// State represents the executor lifecycle state.
type State int

const (
	Idle     State = iota
	Starting       // VM booting (cold start)
	Running        // Agent handling request
	Warm           // VM paused, session cached, idle timeout ticking
	Teardown       // VM shutting down
)

func (s State) String() string {
	switch s {
	case Idle:
		return "IDLE"
	case Starting:
		return "STARTING"
	case Running:
		return "RUNNING"
	case Warm:
		return "WARM"
	case Teardown:
		return "TEARDOWN"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", int(s))
	}
}

// validTransitions defines allowed state transitions.
var validTransitions = map[State][]State{
	Idle:     {Starting},
	Starting: {Running, Teardown},         // Teardown on boot failure
	Running:  {Warm, Teardown},            // Warm on completion, Teardown on error
	Warm:     {Running, Teardown},         // Running on resume, Teardown on idle timeout or eviction
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

// IsAvailable returns true if the executor can accept a /run request.
// This is IDLE (any session) or WARM (same session can resume).
func (sm *StateMachine) IsAvailable() bool {
	s := sm.State()
	return s == Idle || s == Warm
}

// IsIdle returns true if no VM is running.
func (sm *StateMachine) IsIdle() bool {
	return sm.State() == Idle
}

// IsWarm returns true if a VM is paused with a cached session.
func (sm *StateMachine) IsWarm() bool {
	return sm.State() == Warm
}

// Transition attempts to move to the target state.
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
