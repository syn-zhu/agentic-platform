package lifecycle

import (
	"context"
	"fmt"
	"sync"

	"github.com/qmuntal/stateless"
)

// PodLifecycle wraps a stateless.StateMachine to manage the lifecycle of a pod
// through Uninitialized → Configuring → Idle → Booting/Resuming → Ready →
// Executing → TearingDown → Idle. Uses FiringQueued mode with internal storage
// since pod state is ephemeral.
type PodLifecycle struct {
	sm      *stateless.StateMachine
	actions PodActions

	// mu protects execID, sessionID, and readyCh for concurrent access from
	// proxy goroutines. The SM itself serializes trigger processing via
	// FiringQueued, so guards and OnEntry callbacks do NOT need the mutex.
	mu        sync.Mutex
	execID    string
	sessionID string
	readyCh   chan struct{}
}

// NewPodLifecycle creates a new pod lifecycle state machine starting in the
// Uninitialized state. The provided PodActions interface is called for side
// effects during state transitions.
func NewPodLifecycle(actions PodActions) *PodLifecycle {
	pl := &PodLifecycle{
		actions: actions,
		readyCh: make(chan struct{}),
	}

	pl.sm = stateless.NewStateMachineWithMode(PodUninitialized, stateless.FiringQueued)
	pl.configure()
	return pl
}

// State returns the current pod state.
func (pl *PodLifecycle) State(ctx context.Context) (PodState, error) {
	st, err := pl.sm.State(ctx)
	if err != nil {
		return "", err
	}
	return st.(PodState), nil
}

// IsInState returns true if the pod is currently in the given state,
// respecting the superstate/substate hierarchy.
func (pl *PodLifecycle) IsInState(state PodState) (bool, error) {
	return pl.sm.IsInState(state)
}

// Fire fires a trigger on the pod state machine with optional arguments.
func (pl *PodLifecycle) Fire(ctx context.Context, trigger PodTrigger, args ...any) error {
	return pl.sm.FireCtx(ctx, trigger, args...)
}

// ExecID returns the current execution ID. Thread-safe.
func (pl *PodLifecycle) ExecID() string {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	return pl.execID
}

// SessionID returns the current session ID. Thread-safe.
func (pl *PodLifecycle) SessionID() string {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	return pl.sessionID
}

// ReadyCh returns a channel that is closed when the pod enters the GateOpen
// state (Ready or Executing). The channel is reset to a new open channel when
// the pod returns to Idle.
func (pl *PodLifecycle) ReadyCh() <-chan struct{} {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	return pl.readyCh
}

func (pl *PodLifecycle) configure() {
	// --- Uninitialized ---
	pl.sm.Configure(PodUninitialized).
		Permit(TrigConfigDone, PodConfiguring)

	// --- Configuring ---
	pl.sm.Configure(PodConfiguring).
		OnEntry(func(ctx context.Context, _ ...any) error {
			return pl.actions.SetupInfra(ctx)
		}).
		Permit(TrigConfigDone, PodIdle).
		Permit(TrigKill, PodShutdown)

	// --- Idle ---
	pl.sm.Configure(PodIdle).
		OnEntry(func(_ context.Context, _ ...any) error {
			pl.mu.Lock()
			pl.execID = ""
			pl.sessionID = ""
			pl.readyCh = make(chan struct{})
			pl.mu.Unlock()
			return nil
		}).
		Permit(TrigPrepare, PodBooting).
		Permit(TrigKill, PodShutdown)

	// --- Preparing (superstate) ---
	pl.sm.Configure(PodPreparing).
		OnEntry(func(_ context.Context, args ...any) error {
			if len(args) < 2 {
				return fmt.Errorf("Prepare trigger requires execID and sessionID args")
			}
			execID, ok1 := args[0].(string)
			sessionID, ok2 := args[1].(string)
			if !ok1 || !ok2 {
				return fmt.Errorf("Prepare trigger args must be strings")
			}
			pl.mu.Lock()
			pl.execID = execID
			pl.sessionID = sessionID
			pl.mu.Unlock()
			return nil
		}).
		Permit(TrigTimeout, PodTearingDown).
		Permit(TrigPrepareFailed, PodTearingDown).
		Permit(TrigKill, PodTearingDown)

	// --- Booting (substate of Preparing) ---
	pl.sm.Configure(PodBooting).
		SubstateOf(PodPreparing).
		OnEntry(func(ctx context.Context, _ ...any) error {
			return pl.actions.BootVM(ctx)
		}).
		Permit(TrigHealthCheckOK, PodReady)

	// --- Resuming (substate of Preparing) ---
	pl.sm.Configure(PodResuming).
		SubstateOf(PodPreparing).
		OnEntry(func(ctx context.Context, _ ...any) error {
			return pl.actions.ResumeVM(ctx)
		}).
		Permit(TrigHealthCheckOK, PodReady)

	// --- GateOpen (superstate) ---
	pl.sm.Configure(PodGateOpen).
		OnEntry(func(_ context.Context, _ ...any) error {
			pl.mu.Lock()
			close(pl.readyCh)
			pl.mu.Unlock()
			return nil
		}).
		Permit(TrigTimeout, PodTearingDown).
		Permit(TrigKill, PodTearingDown)

	// --- Ready (substate of GateOpen) ---
	pl.sm.Configure(PodReady).
		SubstateOf(PodGateOpen).
		Permit(TrigRunArrived, PodExecuting)

	// --- Executing (substate of GateOpen) ---
	pl.sm.Configure(PodExecuting).
		SubstateOf(PodGateOpen).
		PermitDynamic(TrigExecutionDone, func(_ context.Context, args ...any) (stateless.State, error) {
			if len(args) < 1 {
				return nil, fmt.Errorf("ExecutionDone trigger requires taskState arg")
			}
			taskState, ok := args[0].(string)
			if !ok {
				return nil, fmt.Errorf("ExecutionDone taskState arg must be a string")
			}
			if taskState == "INPUT_REQUIRED" {
				return PodPausing, nil
			}
			return PodTearingDown, nil
		})

	// --- Pausing ---
	pl.sm.Configure(PodPausing).
		OnEntry(func(ctx context.Context, _ ...any) error {
			return pl.actions.PauseVM(ctx)
		}).
		Permit(TrigPauseDone, PodWarm).
		Permit(TrigPrepareFailed, PodTearingDown).
		Permit(TrigKill, PodTearingDown)

	// --- Warm ---
	pl.sm.Configure(PodWarm).
		OnEntry(func(ctx context.Context, _ ...any) error {
			// sessionID is safe to read without mutex here because FiringQueued
			// serializes all trigger processing.
			return pl.actions.RegisterWarm(ctx, pl.sessionID)
		}).
		Permit(TrigPrepare, PodResuming, func(_ context.Context, args ...any) bool {
			// Guard: the incoming session must match the warm session.
			if len(args) < 2 {
				return false
			}
			incomingSession, ok := args[1].(string)
			if !ok {
				return false
			}
			// Safe to read pl.sessionID without mutex — FiringQueued serializes.
			return incomingSession == pl.sessionID
		}).
		Permit(TrigEvict, PodTearingDown).
		Permit(TrigTimeout, PodTearingDown).
		Permit(TrigKill, PodTearingDown)

	// --- TearingDown ---
	pl.sm.Configure(PodTearingDown).
		OnEntry(func(ctx context.Context, _ ...any) error {
			pl.actions.StopVM(ctx)
			pl.actions.CleanupWorkDir(ctx)
			pl.actions.ReleaseLease(ctx)
			return nil
		}).
		Permit(TrigTeardownDone, PodIdle).
		Permit(TrigKill, PodShutdown)

	// --- Shutdown (terminal) ---
	pl.sm.Configure(PodShutdown).
		OnEntry(func(_ context.Context, _ ...any) error {
			pl.actions.CloseAll()
			return nil
		})
}
