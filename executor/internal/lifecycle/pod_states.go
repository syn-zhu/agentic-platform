package lifecycle

import "context"

// PodState represents the state of a pod in the lifecycle state machine.
type PodState string

const (
	PodUninitialized PodState = "Uninitialized"
	PodConfiguring   PodState = "Configuring"
	PodIdle          PodState = "Idle"
	PodPreparing     PodState = "Preparing"  // superstate
	PodBooting       PodState = "Booting"    // substate of Preparing
	PodResuming      PodState = "Resuming"   // substate of Preparing
	PodGateOpen      PodState = "GateOpen"   // superstate
	PodReady         PodState = "Ready"      // substate of GateOpen
	PodExecuting     PodState = "Executing"  // substate of GateOpen
	PodPausing       PodState = "Pausing"
	PodWarm          PodState = "Warm"
	PodTearingDown   PodState = "TearingDown"
	PodShutdown      PodState = "Shutdown"
)

// PodTrigger represents triggers that cause pod state transitions.
type PodTrigger string

const (
	TrigConfigDone    PodTrigger = "ConfigDone"
	TrigPrepare       PodTrigger = "Prepare"       // args: execID, sessionID
	TrigHealthCheckOK PodTrigger = "HealthCheckOK"
	TrigPrepareFailed PodTrigger = "PrepareFailed"
	TrigRunArrived    PodTrigger = "RunArrived"
	TrigExecutionDone PodTrigger = "ExecutionDone" // args: taskState (string)
	TrigPauseDone     PodTrigger = "PauseDone"
	TrigEvict         PodTrigger = "Evict"
	TrigTeardownDone  PodTrigger = "TeardownDone"
	TrigTimeout       PodTrigger = "Timeout"
	TrigKill          PodTrigger = "Kill"
)

// PodActions is the interface the Pod SM calls for side effects.
type PodActions interface {
	SetupInfra(ctx context.Context) error
	BootVM(ctx context.Context) error
	ResumeVM(ctx context.Context) error
	PauseVM(ctx context.Context) error
	StopVM(ctx context.Context)
	CleanupWorkDir(ctx context.Context)
	ReleaseLease(ctx context.Context)
	RegisterWarm(ctx context.Context, sessionID string) error
	CloseAll()
}
