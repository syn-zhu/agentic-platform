//go:build linux

package executor

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/siyanzhu/agentic-platform/executor/internal/config"
	"github.com/siyanzhu/agentic-platform/executor/internal/image"
	"github.com/siyanzhu/agentic-platform/executor/internal/lease"
	"github.com/siyanzhu/agentic-platform/executor/internal/pasta"
	"github.com/siyanzhu/agentic-platform/executor/internal/proxy"
	"github.com/siyanzhu/agentic-platform/executor/internal/vm"
	"github.com/siyanzhu/agentic-platform/executor/internal/vsock"
)

// Runner orchestrates the full execution lifecycle:
// state transitions → boot VM in pasta netns → HTTP to agent on localhost → teardown → release.
type Runner struct {
	cfg        *config.Config
	imgCfg     *image.Config
	sm         *StateMachine
	lease      *lease.Client
	serializer *proxy.ExecutionSerializer
	proxy      *proxy.Proxy
	pasta      *pasta.Instance // Started once at pod startup, reused across executions.
}

// NewRunner creates a runner with the given configuration.
// Sets up pasta and the eBPF proxy (once, for the lifetime of the pod).
func NewRunner(cfg *config.Config, imgCfg *image.Config, sm *StateMachine, leaseClient *lease.Client) (*Runner, error) {
	// Start pasta — creates dedicated netns + cgroup.
	pastaInst, err := pasta.Setup(&pasta.Config{
		AgentPort: imgCfg.Port,
		NsDir:     cfg.WorkloadDir,
	})
	if err != nil {
		return nil, fmt.Errorf("setup pasta: %w", err)
	}

	// Start execution serializer (NoopEventLog for now — replace with MongoDB).
	serializer := proxy.NewExecutionSerializer(proxy.NoopEventLog{})

	// Start eBPF proxy — attaches to pasta's cgroup, listens on :3128.
	p, err := proxy.New(&proxy.Config{
		CgroupPath: pastaInst.CgroupPath(),
		BPFObjPath: filepath.Join(cfg.ImageDir, "connect4.o"),
		Serializer: serializer,
	})
	if err != nil {
		pastaInst.Teardown()
		return nil, fmt.Errorf("setup proxy: %w", err)
	}

	return &Runner{
		cfg:        cfg,
		imgCfg:     imgCfg,
		sm:         sm,
		lease:      leaseClient,
		serializer: serializer,
		proxy:      p,
		pasta:      pastaInst,
	}, nil
}

// Close shuts down the proxy, event log, and pasta.
func (r *Runner) Close() {
	if r.proxy != nil {
		r.proxy.Close()
	}
	if r.serializer != nil {
		r.serializer.Close()
	}
	if r.pasta != nil {
		r.pasta.Teardown()
	}
}

// Run executes a request. Streams SSE data from the agent to w.
// Returns an error if something fails — the caller writes an SSE
// error event.
func (r *Runner) Run(w http.ResponseWriter, claimID, execID string, payload io.Reader) error {
	ctx := context.Background()
	log := slog.With("exec_id", execID, "claim_id", claimID)


	// IDLE → STARTING
	if err := r.sm.Transition(Starting); err != nil {
		return fmt.Errorf("executor busy: %w", err)
	}

	// Ensure we always end up back at IDLE.
	defer func() {
		r.sm.Transition(Teardown)
		r.teardown(ctx, log, claimID, execID)
		r.sm.Transition(Idle)
	}()

	// Start lease renewal.
	leaseCtx, leaseCancel := context.WithCancel(ctx)
	defer leaseCancel()
	r.lease.StartRenewal(leaseCtx, claimID)

	// Prepare vsock server with Init response (config delivery to guest).
	workDir := filepath.Join(r.cfg.WorkloadDir, execID)
	if err := os.MkdirAll(workDir, 0755); err != nil {
		return fmt.Errorf("create work dir: %w", err)
	}

	var guestIP string
	if ips := r.pasta.IPAddresses(); len(ips) > 0 {
		guestIP = ips[0].String()
	}

	initCfg := &vsock.InitConfig{
		Network: &vsock.NetworkConfig{
			IP:        guestIP,
			Gateway:   "169.254.1.1",
			PrefixLen: 32,
			MTU:       1500,
		},
		Files: r.guestFiles(),
	}

	vsockPath := filepath.Join(workDir, "vsock")
	vsockSrv, err := vsock.NewServer(vsockPath, initCfg)
	if err != nil {
		return fmt.Errorf("vsock server: %w", err)
	}
	defer vsockSrv.Close()

	go vsockSrv.Serve()

	// Boot VM in pasta's dedicated netns.
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
		return fmt.Errorf("VM boot failed: %w", err)
	}

	// Wait for agent to be ready.
	agentAddr := fmt.Sprintf("localhost:%d", r.imgCfg.Port)
	log.Info("waiting for agent", "addr", agentAddr)

	if err := waitForAgent(bootCtx, agentAddr); err != nil {
		machine.Stop()
		return fmt.Errorf("agent not ready: %w", err)
	}

	// STARTING → RUNNING
	if err := r.sm.Transition(Running); err != nil {
		machine.Stop()
		return fmt.Errorf("transition to RUNNING: %w", err)
	}

	log.Info("agent ready, forwarding payload")

	// Start execution — persists execution_start and opens the serializer gate.
	// The proxy's RecordExecutionStep calls block until this completes.
	r.serializer.StartExecution(ctx, execID, execID, map[string]any{
		"claim_id": claimID,
	})

	// Send payload to agent via pasta port forwarding.
	agentURL := fmt.Sprintf("http://%s/run", agentAddr)
	agentReq, err := http.NewRequestWithContext(ctx, http.MethodPost, agentURL, payload)
	if err != nil {
		machine.Stop()
		return fmt.Errorf("create agent request: %w", err)
	}
	agentReq.Header.Set("Content-Type", "application/json")
	agentReq.Header.Set("X-Execution-Id", execID)

	agentResp, err := http.DefaultClient.Do(agentReq)
	if err != nil {
		machine.Stop()
		return fmt.Errorf("agent request failed: %w", err)
	}
	defer agentResp.Body.Close()

	// Stream SSE response from agent to waypoint.
	// flushWriter flushes after every Write so SSE events are delivered immediately.
	fw := &flushWriter{w: w, flusher: w.(http.Flusher)}
	if _, err := io.Copy(fw, agentResp.Body); err != nil {
		log.Warn("stream copy error", "error", err)
	}

	// End execution — persists execution_end after all proxy events.
	r.serializer.EndExecution(ctx, "COMPLETED", nil)

	log.Info("execution complete")
	return nil
}

// teardown cleans up after an execution.
func (r *Runner) teardown(ctx context.Context, log *slog.Logger, claimID, execID string) {
	log.Info("tearing down execution")

	workDir := filepath.Join(r.cfg.WorkloadDir, execID)
	if err := os.RemoveAll(workDir); err != nil {
		log.Warn("work dir cleanup error", "error", err)
	}

	if err := r.lease.Release(ctx, claimID); err != nil {
		log.Warn("lease release failed", "error", err)
	}
}

// guestFiles returns the files to inject into the guest filesystem.
func (r *Runner) guestFiles() []vsock.FileConfig {
	var files []vsock.FileConfig

	if data, err := os.ReadFile("/etc/resolv.conf"); err == nil {
		files = append(files, vsock.NewFileConfig("/etc/resolv.conf", data, "0644"))
	}

	files = append(files, vsock.NewFileConfig("/etc/hosts", []byte("127.0.0.1 localhost\n::1 localhost\n"), "0644"))

	return files
}

// flushWriter wraps an http.ResponseWriter and flushes after every Write.
type flushWriter struct {
	w       io.Writer
	flusher http.Flusher
}

func (fw *flushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	if fw.flusher != nil {
		fw.flusher.Flush()
	}
	return n, err
}

// waitForAgent polls the agent's health endpoint until it responds.
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
