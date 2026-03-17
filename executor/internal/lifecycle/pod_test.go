package lifecycle_test

import (
	"context"
	"sync"
	"testing"

	"github.com/siyanzhu/agentic-platform/executor/internal/lifecycle"
)

// mockPodActions records which actions were called for verification.
type mockPodActions struct {
	mu sync.Mutex

	setupInfraCalled    bool
	bootVMCalled        bool
	resumeVMCalled      bool
	pauseVMCalled       bool
	stopVMCalled        bool
	cleanupWorkDirCalled bool
	releaseLeaseCalled  bool
	registerWarmCalled  bool
	registerWarmSession string
	closeAllCalled      bool
}

func (m *mockPodActions) SetupInfra(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setupInfraCalled = true
	return nil
}

func (m *mockPodActions) BootVM(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bootVMCalled = true
	return nil
}

func (m *mockPodActions) ResumeVM(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resumeVMCalled = true
	return nil
}

func (m *mockPodActions) PauseVM(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pauseVMCalled = true
	return nil
}

func (m *mockPodActions) StopVM(_ context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopVMCalled = true
}

func (m *mockPodActions) CleanupWorkDir(_ context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupWorkDirCalled = true
}

func (m *mockPodActions) ReleaseLease(_ context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.releaseLeaseCalled = true
}

func (m *mockPodActions) RegisterWarm(_ context.Context, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.registerWarmCalled = true
	m.registerWarmSession = sessionID
	return nil
}

func (m *mockPodActions) CloseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeAllCalled = true
}

func newTestPod() (*lifecycle.PodLifecycle, *mockPodActions) {
	actions := &mockPodActions{}
	pl := lifecycle.NewPodLifecycle(actions)
	return pl, actions
}

func mustPodState(t *testing.T, pl *lifecycle.PodLifecycle) lifecycle.PodState {
	t.Helper()
	st, err := pl.State(context.Background())
	if err != nil {
		t.Fatalf("failed to get state: %v", err)
	}
	return st
}

func TestPodInitialStateIsUninitialized(t *testing.T) {
	pl, _ := newTestPod()
	if got := mustPodState(t, pl); got != lifecycle.PodUninitialized {
		t.Fatalf("expected Uninitialized, got %s", got)
	}
}

func TestPodFullBootSequence(t *testing.T) {
	pl, _ := newTestPod()
	ctx := context.Background()

	// Uninitialized → Configuring
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if got := mustPodState(t, pl); got != lifecycle.PodConfiguring {
		t.Fatalf("expected Configuring, got %s", got)
	}

	// Configuring → Idle
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if got := mustPodState(t, pl); got != lifecycle.PodIdle {
		t.Fatalf("expected Idle, got %s", got)
	}

	// Idle → Booting (via Prepare)
	if err := pl.Fire(ctx, lifecycle.TrigPrepare, "exec-1", "session-1"); err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	if got := mustPodState(t, pl); got != lifecycle.PodBooting {
		t.Fatalf("expected Booting, got %s", got)
	}

	// Booting → Ready (via HealthCheckOK)
	if err := pl.Fire(ctx, lifecycle.TrigHealthCheckOK); err != nil {
		t.Fatalf("HealthCheckOK failed: %v", err)
	}
	if got := mustPodState(t, pl); got != lifecycle.PodReady {
		t.Fatalf("expected Ready, got %s", got)
	}

	// Ready → Executing (via RunArrived)
	if err := pl.Fire(ctx, lifecycle.TrigRunArrived); err != nil {
		t.Fatalf("RunArrived failed: %v", err)
	}
	if got := mustPodState(t, pl); got != lifecycle.PodExecuting {
		t.Fatalf("expected Executing, got %s", got)
	}
}

func TestPodIsInStateGateOpen(t *testing.T) {
	pl, _ := newTestPod()
	ctx := context.Background()

	// Get to Ready state.
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigPrepare, "exec-1", "session-1"); err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigHealthCheckOK); err != nil {
		t.Fatalf("HealthCheckOK failed: %v", err)
	}

	// Ready is a substate of GateOpen.
	isGateOpen, err := pl.IsInState(lifecycle.PodGateOpen)
	if err != nil {
		t.Fatalf("IsInState failed: %v", err)
	}
	if !isGateOpen {
		t.Fatal("expected IsInState(GateOpen) to be true for Ready")
	}

	// Move to Executing.
	if err := pl.Fire(ctx, lifecycle.TrigRunArrived); err != nil {
		t.Fatalf("RunArrived failed: %v", err)
	}

	isGateOpen, err = pl.IsInState(lifecycle.PodGateOpen)
	if err != nil {
		t.Fatalf("IsInState failed: %v", err)
	}
	if !isGateOpen {
		t.Fatal("expected IsInState(GateOpen) to be true for Executing")
	}
}

func TestPodTeardownCycle(t *testing.T) {
	pl, actions := newTestPod()
	ctx := context.Background()

	// Get to Executing.
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigPrepare, "exec-1", "session-1"); err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigHealthCheckOK); err != nil {
		t.Fatalf("HealthCheckOK failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigRunArrived); err != nil {
		t.Fatalf("RunArrived failed: %v", err)
	}

	// ExecutionDone("COMPLETED") → TearingDown
	if err := pl.Fire(ctx, lifecycle.TrigExecutionDone, "COMPLETED"); err != nil {
		t.Fatalf("ExecutionDone failed: %v", err)
	}
	if got := mustPodState(t, pl); got != lifecycle.PodTearingDown {
		t.Fatalf("expected TearingDown, got %s", got)
	}

	// Verify teardown actions called.
	actions.mu.Lock()
	if !actions.stopVMCalled {
		t.Error("expected StopVM to be called")
	}
	if !actions.cleanupWorkDirCalled {
		t.Error("expected CleanupWorkDir to be called")
	}
	if !actions.releaseLeaseCalled {
		t.Error("expected ReleaseLease to be called")
	}
	actions.mu.Unlock()

	// TeardownDone → Idle
	if err := pl.Fire(ctx, lifecycle.TrigTeardownDone); err != nil {
		t.Fatalf("TeardownDone failed: %v", err)
	}
	if got := mustPodState(t, pl); got != lifecycle.PodIdle {
		t.Fatalf("expected Idle, got %s", got)
	}
}

func TestPodWarmPausePath(t *testing.T) {
	pl, actions := newTestPod()
	ctx := context.Background()

	// Get to Executing.
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigPrepare, "exec-1", "session-1"); err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigHealthCheckOK); err != nil {
		t.Fatalf("HealthCheckOK failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigRunArrived); err != nil {
		t.Fatalf("RunArrived failed: %v", err)
	}

	// ExecutionDone("INPUT_REQUIRED") → Pausing
	if err := pl.Fire(ctx, lifecycle.TrigExecutionDone, "INPUT_REQUIRED"); err != nil {
		t.Fatalf("ExecutionDone failed: %v", err)
	}
	if got := mustPodState(t, pl); got != lifecycle.PodPausing {
		t.Fatalf("expected Pausing, got %s", got)
	}

	// Verify PauseVM called.
	actions.mu.Lock()
	if !actions.pauseVMCalled {
		t.Error("expected PauseVM to be called")
	}
	actions.mu.Unlock()

	// PauseDone → Warm
	if err := pl.Fire(ctx, lifecycle.TrigPauseDone); err != nil {
		t.Fatalf("PauseDone failed: %v", err)
	}
	if got := mustPodState(t, pl); got != lifecycle.PodWarm {
		t.Fatalf("expected Warm, got %s", got)
	}

	// Verify RegisterWarm called with correct session.
	actions.mu.Lock()
	if !actions.registerWarmCalled {
		t.Error("expected RegisterWarm to be called")
	}
	if actions.registerWarmSession != "session-1" {
		t.Errorf("expected RegisterWarm sessionID=session-1, got %s", actions.registerWarmSession)
	}
	actions.mu.Unlock()
}

func TestPodWarmResumeMatchingSession(t *testing.T) {
	pl, _ := newTestPod()
	ctx := context.Background()

	// Get to Warm with session-1.
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigPrepare, "exec-1", "session-1"); err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigHealthCheckOK); err != nil {
		t.Fatalf("HealthCheckOK failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigRunArrived); err != nil {
		t.Fatalf("RunArrived failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigExecutionDone, "INPUT_REQUIRED"); err != nil {
		t.Fatalf("ExecutionDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigPauseDone); err != nil {
		t.Fatalf("PauseDone failed: %v", err)
	}
	if got := mustPodState(t, pl); got != lifecycle.PodWarm {
		t.Fatalf("expected Warm, got %s", got)
	}

	// Resume with matching session should succeed.
	if err := pl.Fire(ctx, lifecycle.TrigPrepare, "exec-2", "session-1"); err != nil {
		t.Fatalf("Prepare with matching session failed: %v", err)
	}
	if got := mustPodState(t, pl); got != lifecycle.PodResuming {
		t.Fatalf("expected Resuming, got %s", got)
	}
}

func TestPodWarmResumeWrongSessionFails(t *testing.T) {
	pl, _ := newTestPod()
	ctx := context.Background()

	// Get to Warm with session-1.
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigPrepare, "exec-1", "session-1"); err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigHealthCheckOK); err != nil {
		t.Fatalf("HealthCheckOK failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigRunArrived); err != nil {
		t.Fatalf("RunArrived failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigExecutionDone, "INPUT_REQUIRED"); err != nil {
		t.Fatalf("ExecutionDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigPauseDone); err != nil {
		t.Fatalf("PauseDone failed: %v", err)
	}

	// Resume with wrong session should fail.
	err := pl.Fire(ctx, lifecycle.TrigPrepare, "exec-2", "session-WRONG")
	if err == nil {
		t.Fatal("expected Prepare with wrong session to fail, but it succeeded")
	}

	// Should still be in Warm.
	if got := mustPodState(t, pl); got != lifecycle.PodWarm {
		t.Fatalf("expected Warm, got %s", got)
	}
}

func TestPodKillFromIdleToShutdown(t *testing.T) {
	pl, actions := newTestPod()
	ctx := context.Background()

	// Get to Idle.
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if got := mustPodState(t, pl); got != lifecycle.PodIdle {
		t.Fatalf("expected Idle, got %s", got)
	}

	// Kill → Shutdown
	if err := pl.Fire(ctx, lifecycle.TrigKill); err != nil {
		t.Fatalf("Kill failed: %v", err)
	}
	if got := mustPodState(t, pl); got != lifecycle.PodShutdown {
		t.Fatalf("expected Shutdown, got %s", got)
	}

	actions.mu.Lock()
	if !actions.closeAllCalled {
		t.Error("expected CloseAll to be called")
	}
	actions.mu.Unlock()
}

func TestPodTimeoutFromReadyToTearingDown(t *testing.T) {
	pl, _ := newTestPod()
	ctx := context.Background()

	// Get to Ready.
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigPrepare, "exec-1", "session-1"); err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigHealthCheckOK); err != nil {
		t.Fatalf("HealthCheckOK failed: %v", err)
	}
	if got := mustPodState(t, pl); got != lifecycle.PodReady {
		t.Fatalf("expected Ready, got %s", got)
	}

	// Timeout → TearingDown (via GateOpen superstate)
	if err := pl.Fire(ctx, lifecycle.TrigTimeout); err != nil {
		t.Fatalf("Timeout failed: %v", err)
	}
	if got := mustPodState(t, pl); got != lifecycle.PodTearingDown {
		t.Fatalf("expected TearingDown, got %s", got)
	}
}

func TestPodReadyChannelClosedAfterGateOpen(t *testing.T) {
	pl, _ := newTestPod()
	ctx := context.Background()

	// Get to Ready (GateOpen).
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigPrepare, "exec-1", "session-1"); err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigHealthCheckOK); err != nil {
		t.Fatalf("HealthCheckOK failed: %v", err)
	}

	// Ready channel should be closed (non-blocking receive).
	ch := pl.ReadyCh()
	select {
	case <-ch:
		// OK, channel is closed.
	default:
		t.Fatal("expected Ready channel to be closed after entering GateOpen")
	}
}

func TestPodReadyChannelResetAfterReturningToIdle(t *testing.T) {
	pl, _ := newTestPod()
	ctx := context.Background()

	// Full cycle: boot → execute → teardown → idle.
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigPrepare, "exec-1", "session-1"); err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigHealthCheckOK); err != nil {
		t.Fatalf("HealthCheckOK failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigRunArrived); err != nil {
		t.Fatalf("RunArrived failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigExecutionDone, "COMPLETED"); err != nil {
		t.Fatalf("ExecutionDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigTeardownDone); err != nil {
		t.Fatalf("TeardownDone failed: %v", err)
	}
	if got := mustPodState(t, pl); got != lifecycle.PodIdle {
		t.Fatalf("expected Idle, got %s", got)
	}

	// Ready channel should be blocking (reset).
	ch := pl.ReadyCh()
	select {
	case <-ch:
		t.Fatal("expected Ready channel to be blocking after returning to Idle")
	default:
		// OK, channel is open (blocking).
	}
}

func TestPodSetupInfraCalledDuringConfiguring(t *testing.T) {
	pl, actions := newTestPod()
	ctx := context.Background()

	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}

	actions.mu.Lock()
	if !actions.setupInfraCalled {
		t.Error("expected SetupInfra to be called during Configuring")
	}
	actions.mu.Unlock()
}

func TestPodBootVMCalledDuringBooting(t *testing.T) {
	pl, actions := newTestPod()
	ctx := context.Background()

	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigPrepare, "exec-1", "session-1"); err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}

	actions.mu.Lock()
	if !actions.bootVMCalled {
		t.Error("expected BootVM to be called during Booting")
	}
	actions.mu.Unlock()
}

func TestPodPauseVMCalledDuringPausing(t *testing.T) {
	pl, actions := newTestPod()
	ctx := context.Background()

	// Get to Executing.
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigPrepare, "exec-1", "session-1"); err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigHealthCheckOK); err != nil {
		t.Fatalf("HealthCheckOK failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigRunArrived); err != nil {
		t.Fatalf("RunArrived failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigExecutionDone, "INPUT_REQUIRED"); err != nil {
		t.Fatalf("ExecutionDone failed: %v", err)
	}

	actions.mu.Lock()
	if !actions.pauseVMCalled {
		t.Error("expected PauseVM to be called during Pausing")
	}
	actions.mu.Unlock()
}

func TestPodTeardownActionsCalledDuringTearingDown(t *testing.T) {
	pl, actions := newTestPod()
	ctx := context.Background()

	// Get to Ready and then timeout to trigger teardown.
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigPrepare, "exec-1", "session-1"); err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigHealthCheckOK); err != nil {
		t.Fatalf("HealthCheckOK failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigTimeout); err != nil {
		t.Fatalf("Timeout failed: %v", err)
	}

	actions.mu.Lock()
	if !actions.stopVMCalled {
		t.Error("expected StopVM to be called during TearingDown")
	}
	if !actions.cleanupWorkDirCalled {
		t.Error("expected CleanupWorkDir to be called during TearingDown")
	}
	if !actions.releaseLeaseCalled {
		t.Error("expected ReleaseLease to be called during TearingDown")
	}
	actions.mu.Unlock()
}

func TestPodRegisterWarmCalledWithCorrectSession(t *testing.T) {
	pl, actions := newTestPod()
	ctx := context.Background()

	// Get to Warm.
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigPrepare, "exec-1", "my-session"); err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigHealthCheckOK); err != nil {
		t.Fatalf("HealthCheckOK failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigRunArrived); err != nil {
		t.Fatalf("RunArrived failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigExecutionDone, "INPUT_REQUIRED"); err != nil {
		t.Fatalf("ExecutionDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigPauseDone); err != nil {
		t.Fatalf("PauseDone failed: %v", err)
	}

	actions.mu.Lock()
	if !actions.registerWarmCalled {
		t.Error("expected RegisterWarm to be called")
	}
	if actions.registerWarmSession != "my-session" {
		t.Errorf("expected RegisterWarm sessionID=my-session, got %s", actions.registerWarmSession)
	}
	actions.mu.Unlock()
}

func TestPodExecIDAndSessionID(t *testing.T) {
	pl, _ := newTestPod()
	ctx := context.Background()

	// Get to Booting with specific exec/session IDs.
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigPrepare, "exec-42", "session-99"); err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}

	if got := pl.ExecID(); got != "exec-42" {
		t.Fatalf("expected ExecID=exec-42, got %s", got)
	}
	if got := pl.SessionID(); got != "session-99" {
		t.Fatalf("expected SessionID=session-99, got %s", got)
	}
}

func TestPodExecIDClearedOnReturnToIdle(t *testing.T) {
	pl, _ := newTestPod()
	ctx := context.Background()

	// Full cycle back to Idle.
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigPrepare, "exec-1", "session-1"); err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigHealthCheckOK); err != nil {
		t.Fatalf("HealthCheckOK failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigRunArrived); err != nil {
		t.Fatalf("RunArrived failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigExecutionDone, "COMPLETED"); err != nil {
		t.Fatalf("ExecutionDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigTeardownDone); err != nil {
		t.Fatalf("TeardownDone failed: %v", err)
	}

	if got := pl.ExecID(); got != "" {
		t.Fatalf("expected ExecID to be cleared, got %s", got)
	}
	if got := pl.SessionID(); got != "" {
		t.Fatalf("expected SessionID to be cleared, got %s", got)
	}
}

func TestPodKillFromConfiguringToShutdown(t *testing.T) {
	pl, actions := newTestPod()
	ctx := context.Background()

	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if got := mustPodState(t, pl); got != lifecycle.PodConfiguring {
		t.Fatalf("expected Configuring, got %s", got)
	}

	if err := pl.Fire(ctx, lifecycle.TrigKill); err != nil {
		t.Fatalf("Kill failed: %v", err)
	}
	if got := mustPodState(t, pl); got != lifecycle.PodShutdown {
		t.Fatalf("expected Shutdown, got %s", got)
	}

	actions.mu.Lock()
	if !actions.closeAllCalled {
		t.Error("expected CloseAll to be called")
	}
	actions.mu.Unlock()
}

func TestPodKillFromTearingDownToShutdown(t *testing.T) {
	pl, _ := newTestPod()
	ctx := context.Background()

	// Get to TearingDown.
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigPrepare, "exec-1", "session-1"); err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigHealthCheckOK); err != nil {
		t.Fatalf("HealthCheckOK failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigTimeout); err != nil {
		t.Fatalf("Timeout failed: %v", err)
	}
	if got := mustPodState(t, pl); got != lifecycle.PodTearingDown {
		t.Fatalf("expected TearingDown, got %s", got)
	}

	// Kill from TearingDown → Shutdown.
	if err := pl.Fire(ctx, lifecycle.TrigKill); err != nil {
		t.Fatalf("Kill failed: %v", err)
	}
	if got := mustPodState(t, pl); got != lifecycle.PodShutdown {
		t.Fatalf("expected Shutdown, got %s", got)
	}
}

func TestPodEvictFromWarmToTearingDown(t *testing.T) {
	pl, _ := newTestPod()
	ctx := context.Background()

	// Get to Warm.
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigPrepare, "exec-1", "session-1"); err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigHealthCheckOK); err != nil {
		t.Fatalf("HealthCheckOK failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigRunArrived); err != nil {
		t.Fatalf("RunArrived failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigExecutionDone, "INPUT_REQUIRED"); err != nil {
		t.Fatalf("ExecutionDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigPauseDone); err != nil {
		t.Fatalf("PauseDone failed: %v", err)
	}

	// Evict → TearingDown.
	if err := pl.Fire(ctx, lifecycle.TrigEvict); err != nil {
		t.Fatalf("Evict failed: %v", err)
	}
	if got := mustPodState(t, pl); got != lifecycle.PodTearingDown {
		t.Fatalf("expected TearingDown, got %s", got)
	}
}

func TestPodResumeVMCalledDuringResuming(t *testing.T) {
	pl, actions := newTestPod()
	ctx := context.Background()

	// Get to Warm and then resume.
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigPrepare, "exec-1", "session-1"); err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigHealthCheckOK); err != nil {
		t.Fatalf("HealthCheckOK failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigRunArrived); err != nil {
		t.Fatalf("RunArrived failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigExecutionDone, "INPUT_REQUIRED"); err != nil {
		t.Fatalf("ExecutionDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigPauseDone); err != nil {
		t.Fatalf("PauseDone failed: %v", err)
	}

	// Resume.
	if err := pl.Fire(ctx, lifecycle.TrigPrepare, "exec-2", "session-1"); err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}

	actions.mu.Lock()
	if !actions.resumeVMCalled {
		t.Error("expected ResumeVM to be called during Resuming")
	}
	actions.mu.Unlock()
}

func TestPodPreparingCapturesExecIDAndSessionID(t *testing.T) {
	pl, _ := newTestPod()
	ctx := context.Background()

	// Get to Idle.
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}

	// Prepare with args.
	if err := pl.Fire(ctx, lifecycle.TrigPrepare, "exec-abc", "session-xyz"); err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}

	if got := pl.ExecID(); got != "exec-abc" {
		t.Fatalf("expected ExecID=exec-abc, got %s", got)
	}
	if got := pl.SessionID(); got != "session-xyz" {
		t.Fatalf("expected SessionID=session-xyz, got %s", got)
	}
}

func TestPodTimeoutFromPreparingToTearingDown(t *testing.T) {
	pl, _ := newTestPod()
	ctx := context.Background()

	// Get to Booting (substate of Preparing).
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigPrepare, "exec-1", "session-1"); err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	if got := mustPodState(t, pl); got != lifecycle.PodBooting {
		t.Fatalf("expected Booting, got %s", got)
	}

	// Timeout from Preparing superstate → TearingDown.
	if err := pl.Fire(ctx, lifecycle.TrigTimeout); err != nil {
		t.Fatalf("Timeout failed: %v", err)
	}
	if got := mustPodState(t, pl); got != lifecycle.PodTearingDown {
		t.Fatalf("expected TearingDown, got %s", got)
	}
}

func TestPodPrepareFailedFromBootingToTearingDown(t *testing.T) {
	pl, _ := newTestPod()
	ctx := context.Background()

	// Get to Booting.
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigConfigDone); err != nil {
		t.Fatalf("ConfigDone failed: %v", err)
	}
	if err := pl.Fire(ctx, lifecycle.TrigPrepare, "exec-1", "session-1"); err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}

	// PrepareFailed → TearingDown.
	if err := pl.Fire(ctx, lifecycle.TrigPrepareFailed); err != nil {
		t.Fatalf("PrepareFailed failed: %v", err)
	}
	if got := mustPodState(t, pl); got != lifecycle.PodTearingDown {
		t.Fatalf("expected TearingDown, got %s", got)
	}
}
