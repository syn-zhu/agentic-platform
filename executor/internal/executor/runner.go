//go:build linux

package executor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/siyanzhu/agentic-platform/executor/internal/config"
	"github.com/siyanzhu/agentic-platform/executor/internal/image"
	"github.com/siyanzhu/agentic-platform/executor/internal/lease"
	"github.com/siyanzhu/agentic-platform/executor/internal/pasta"
	"github.com/siyanzhu/agentic-platform/executor/internal/vm"
	"github.com/siyanzhu/agentic-platform/executor/internal/vsock"
	initpb "github.com/siyanzhu/agentic-platform/executor/internal/vsock/initpb"
)

// Runner orchestrates the full execution lifecycle including warm VM reuse.
type Runner struct {
	cfg    *config.Config
	imgCfg *image.Config
	sm     *StateMachine
	lease  *lease.Client
	pasta  *pasta.Instance

	// Warm VM state (protected by mu).
	mu        sync.Mutex
	machine   *vm.VM     // Current VM (nil when IDLE)
	sessionID string     // Session cached in the warm VM
	idleTimer *time.Timer // Fires when warm timeout expires
}

// NewRunner creates a runner. Starts pasta once for the pod's lifetime.
func NewRunner(cfg *config.Config, imgCfg *image.Config, sm *StateMachine, leaseClient *lease.Client) (*Runner, error) {
	pastaInst, err := pasta.Setup(&pasta.Config{
		AgentPort: imgCfg.Port,
		NsDir:     cfg.WorkloadDir,
	})
	if err != nil {
		return nil, fmt.Errorf("setup pasta: %w", err)
	}

	return &Runner{
		cfg:    cfg,
		imgCfg: imgCfg,
		sm:     sm,
		lease:  leaseClient,
		pasta:  pastaInst,
	}, nil
}

// Close tears down pasta and any running VM.
func (r *Runner) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.idleTimer != nil {
		r.idleTimer.Stop()
	}
	if r.machine != nil {
		r.machine.Stop()
		r.machine = nil
	}
	if r.pasta != nil {
		r.pasta.Teardown()
	}
}

// Run handles a /run request. Behavior depends on current state:
//   - IDLE: cold start (boot VM, then forward request)
//   - WARM + matching session: warm resume (unpause VM, forward request)
//   - WARM + different session: evict (teardown warm VM, then cold start)
func (r *Runner) Run(w http.ResponseWriter, claimID, execID, sessionID string, warm bool, payload io.Reader) error {
	ctx := context.Background()
	log := slog.With("exec_id", execID, "claim_id", claimID, "session_id", sessionID)

	payloadBytes, err := io.ReadAll(payload)
	if err != nil {
		return fmt.Errorf("read payload: %w", err)
	}

	// Start lease renewal.
	leaseCtx, leaseCancel := context.WithCancel(ctx)
	defer leaseCancel()
	r.lease.StartRenewal(leaseCtx, claimID)

	// Determine execution path based on state.
	if warm && r.sm.IsWarm() && r.matchesSession(sessionID) {
		// Warm resume — unpause the existing VM.
		log.Info("warm resume")
		r.stopIdleTimer()

		if err := r.sm.Transition(Running); err != nil {
			return fmt.Errorf("transition WARM→RUNNING: %w", err)
		}

		if err := r.machine.Unpause(ctx); err != nil {
			r.teardownVM(log)
			return fmt.Errorf("unpause: %w", err)
		}
	} else {
		// Cold start — evict warm VM if present, then boot fresh.
		if r.sm.IsWarm() {
			log.Info("evicting warm session", "old_session", r.sessionID)
			r.stopIdleTimer()
			r.teardownVM(log)
		}

		if err := r.sm.Transition(Starting); err != nil {
			return fmt.Errorf("transition to STARTING: %w", err)
		}

		if err := r.bootVM(ctx, log, execID); err != nil {
			r.sm.Transition(Teardown)
			r.sm.Transition(Idle)
			return err
		}

		if err := r.sm.Transition(Running); err != nil {
			r.teardownVM(log)
			return fmt.Errorf("transition STARTING→RUNNING: %w", err)
		}
	}

	// Forward payload to agent and stream response.
	r.setSession(sessionID)
	err = r.proxyRequest(ctx, log, w, execID, payloadBytes)

	// Pause VM and transition to WARM (unless error requires teardown).
	if err != nil {
		log.Error("execution error, tearing down", "error", err)
		r.teardownVM(log)
		r.lease.Release(ctx, claimID)
		return err
	}

	// Pause the VM — session stays cached in memory.
	if pauseErr := r.machine.Pause(ctx); pauseErr != nil {
		log.Error("pause failed, tearing down", "error", pauseErr)
		r.teardownVM(log)
		r.lease.Release(ctx, claimID)
		return nil
	}

	r.sm.Transition(Warm)
	r.startIdleTimer(log)
	log.Info("VM paused, entering WARM state", "timeout", r.cfg.WarmTimeout)

	// Release the claim — pod returns to pool as warm.
	r.lease.Release(ctx, claimID)
	return nil
}

// bootVM starts a new Firecracker VM in pasta's netns.
func (r *Runner) bootVM(ctx context.Context, log *slog.Logger, execID string) error {
	log.Info("booting VM (cold start)")

	workDir := filepath.Join(r.cfg.WorkloadDir, execID)
	if err := os.MkdirAll(workDir, 0755); err != nil {
		return fmt.Errorf("create work dir: %w", err)
	}

	// vsock for config delivery.
	var guestIP string
	if ips := r.pasta.IPAddresses(); len(ips) > 0 {
		guestIP = ips[0].String()
	}
	initResp := &initpb.InitResponse{
		Network: &initpb.NetworkConfig{Ip: guestIP},
		Files:   r.guestFiles(),
	}
	vsockPath := filepath.Join(workDir, "vsock")
	vsockSrv, err := vsock.NewServer(vsockPath, initResp)
	if err != nil {
		return fmt.Errorf("vsock server: %w", err)
	}
	defer vsockSrv.Close()
	go vsockSrv.Serve(ctx)

	// Boot VM.
	bootCtx, bootCancel := context.WithTimeout(ctx, r.cfg.BootTimeout)
	defer bootCancel()

	vmCfg := vm.Config{
		KernelPath: filepath.Join(r.cfg.ImageDir, "vmlinux"),
		InitrdPath: filepath.Join(r.cfg.ImageDir, "initramfs.cpio.lz4"),
		RootfsPath: filepath.Join(r.cfg.ImageDir, "rootfs.ext4"),
		TAPName:    "eth0",
		VCPUs:      r.cfg.VCPUs,
		MemoryMB:   r.cfg.MemoryMB,
		WorkDir:    workDir,
		VsockPath:  vsockPath,
		NsPath:     r.pasta.NsPath(),
	}

	machine, err := vm.Boot(bootCtx, vmCfg)
	if err != nil {
		return fmt.Errorf("VM boot: %w", err)
	}

	// Wait for agent.
	agentAddr := fmt.Sprintf("localhost:%d", r.imgCfg.Port)
	if err := waitForAgent(bootCtx, agentAddr); err != nil {
		machine.Stop()
		return fmt.Errorf("agent not ready: %w", err)
	}

	r.mu.Lock()
	r.machine = machine
	r.mu.Unlock()

	log.Info("VM booted, agent ready")
	return nil
}

// proxyRequest forwards the payload to the agent and streams the SSE response.
func (r *Runner) proxyRequest(ctx context.Context, log *slog.Logger, w http.ResponseWriter, execID string, payloadBytes []byte) error {
	agentAddr := fmt.Sprintf("localhost:%d", r.imgCfg.Port)
	agentURL := fmt.Sprintf("http://%s/run", agentAddr)

	agentReq, err := http.NewRequestWithContext(ctx, http.MethodPost, agentURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return fmt.Errorf("create agent request: %w", err)
	}
	agentReq.Header.Set("Content-Type", "application/json")
	agentReq.Header.Set("X-Execution-Id", execID)

	agentResp, err := http.DefaultClient.Do(agentReq)
	if err != nil {
		return fmt.Errorf("agent request: %w", err)
	}
	defer agentResp.Body.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 4096)

	for {
		n, readErr := agentResp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				log.Warn("client disconnected", "error", writeErr)
				return nil
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				return fmt.Errorf("agent read: %w", readErr)
			}
			break
		}
	}

	log.Info("execution complete")
	return nil
}

// teardownVM kills the current VM and transitions to IDLE.
func (r *Runner) teardownVM(log *slog.Logger) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.machine != nil {
		r.machine.Stop()
		r.machine = nil
	}
	r.sessionID = ""
	r.sm.Transition(Teardown)
	r.sm.Transition(Idle)
	log.Info("VM torn down")
}

// setSession records the session ID for the current warm VM.
func (r *Runner) setSession(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessionID = id
}

// matchesSession checks if the warm VM holds the given session.
func (r *Runner) matchesSession(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessionID == id
}

// startIdleTimer starts the warm timeout. When it fires, the VM is torn down.
func (r *Runner) startIdleTimer(log *slog.Logger) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.idleTimer = time.AfterFunc(r.cfg.WarmTimeout, func() {
		log.Info("warm timeout expired, tearing down")
		r.teardownVM(log)
	})
}

// stopIdleTimer cancels the warm timeout.
func (r *Runner) stopIdleTimer() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.idleTimer != nil {
		r.idleTimer.Stop()
		r.idleTimer = nil
	}
}

// guestFiles returns files to inject into the guest filesystem.
func (r *Runner) guestFiles() []*initpb.FileConfig {
	var files []*initpb.FileConfig
	if data, err := os.ReadFile("/etc/resolv.conf"); err == nil {
		files = append(files, &initpb.FileConfig{
			Path: "/etc/resolv.conf", Content: data, Mode: 0644,
		})
	}
	files = append(files, &initpb.FileConfig{
		Path: "/etc/hosts", Content: []byte("127.0.0.1 localhost\n::1 localhost\n"), Mode: 0644,
	})
	return files
}

// waitForAgent polls the agent's health endpoint.
func waitForAgent(ctx context.Context, addr string) error {
	client := &http.Client{Timeout: 1 * time.Second}
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for agent at %s", addr)
		default:
		}
		resp, err := client.Get(fmt.Sprintf("http://%s/healthz", addr))
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
}
