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

	"github.com/siyanzhu/agentic-platform/executor/internal/config"
	"github.com/siyanzhu/agentic-platform/executor/internal/lease"
	execnet "github.com/siyanzhu/agentic-platform/executor/internal/net"
	"github.com/siyanzhu/agentic-platform/executor/internal/vm"
	"github.com/siyanzhu/agentic-platform/executor/internal/vsock"
	initpb "github.com/siyanzhu/agentic-platform/executor/internal/vsock/initpb"
)

// Runner orchestrates the full execution lifecycle:
// state transitions → TAP setup → VM boot → vsock Init/Stream → teardown → release.
type Runner struct {
	cfg    *config.Config
	sm     *StateMachine
	lease  *lease.Client
	netCfg *execnet.Config
}

// NewRunner creates a runner with the given configuration.
func NewRunner(cfg *config.Config, sm *StateMachine, leaseClient *lease.Client) *Runner {
	return &Runner{
		cfg:    cfg,
		sm:     sm,
		lease:  leaseClient,
		netCfg: execnet.DefaultConfig(),
	}
}

// Run implements server.Runner. Blocks until execution completes,
// streaming SSE chunks to w.
func (r *Runner) Run(w http.ResponseWriter, claimID, execID string, payload io.Reader) error {
	ctx := context.Background()
	log := slog.With("exec_id", execID, "claim_id", claimID)

	// Read payload.
	payloadBytes, err := io.ReadAll(payload)
	if err != nil {
		return fmt.Errorf("read payload: %w", err)
	}

	// IDLE → STARTING
	if err := r.sm.Transition(Starting); err != nil {
		return fmt.Errorf("transition to STARTING: %w", err)
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

	// Set up routed TAP.
	if err := execnet.Setup(r.netCfg); err != nil {
		return fmt.Errorf("TAP setup: %w", err)
	}

	// Prepare vsock server with Init response.
	workDir := filepath.Join(config.WorkloadDir, execID)
	if err := os.MkdirAll(workDir, 0755); err != nil {
		return fmt.Errorf("create work dir: %w", err)
	}

	guestNet := r.netCfg.GuestNetworkConfig()
	initResp := &initpb.InitResponse{
		Network: &initpb.NetworkConfig{
			Ip:        guestNet.IP,
			Gateway:   guestNet.Gateway,
			PrefixLen: int32(guestNet.PrefixLen),
			Mtu:       int32(guestNet.MTU),
		},
		Files:   r.guestFiles(),
		Payload: payloadBytes,
		PayloadHeaders: map[string]string{
			"Content-Type": "application/json",
		},
		AgentPort: int32(config.AgentPort),
	}

	vsockPath := filepath.Join(workDir, "vsock")
	vsockSrv, err := vsock.NewServer(vsockPath, initResp)
	if err != nil {
		return fmt.Errorf("vsock server: %w", err)
	}
	defer vsockSrv.Close()

	// Start vsock server in background (accepts guest connections).
	go vsockSrv.Serve(ctx)

	// Boot VM.
	bootCtx, bootCancel := context.WithTimeout(ctx, r.cfg.BootTimeout)
	defer bootCancel()

	vmCfg := vm.Config{
		KernelPath: filepath.Join(config.ImageDir, "vmlinux"),
		RootfsPath: filepath.Join(config.ImageDir, "rootfs.ext4"),
		TAPName:    r.netCfg.TAPName,
		VCPUs:      r.cfg.VCPUs,
		MemoryMB:   parseMemoryMB(r.cfg.Memory),
		WorkDir:    workDir,
		VsockCID:   3,
	}

	machine, err := vm.Boot(bootCtx, vmCfg)
	if err != nil {
		return fmt.Errorf("VM boot: %w", err)
	}

	log.Info("VM booted, waiting for guest Init + Stream")

	// STARTING → RUNNING
	// The guest will call Init (gets config + payload), configure itself,
	// start the agent, forward the payload, then call Stream repeatedly
	// with SSE chunks. We proxy those chunks to the HTTP response.
	if err := r.sm.Transition(Running); err != nil {
		machine.Stop()
		return fmt.Errorf("transition to RUNNING: %w", err)
	}

	// Set SSE headers.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Proxy SSE chunks from vsock to HTTP response.
	flusher, _ := w.(http.Flusher)

	execCtx, execCancel := context.WithTimeout(ctx, r.cfg.ExecTimeout)
	defer execCancel()

	for {
		select {
		case chunk, ok := <-vsockSrv.StreamCh():
			if !ok {
				// Channel closed.
				log.Info("stream channel closed")
				return nil
			}
			if chunk.Error != "" {
				log.Error("agent error", "error", chunk.Error)
				return fmt.Errorf("agent error: %s", chunk.Error)
			}
			if chunk.Done {
				log.Info("stream complete")
				return nil
			}
			if _, err := w.Write(chunk.Data); err != nil {
				log.Warn("write to client failed", "error", err)
				return nil // Client disconnected.
			}
			if flusher != nil {
				flusher.Flush()
			}

		case <-execCtx.Done():
			log.Warn("execution timeout")
			machine.Stop()
			return fmt.Errorf("execution timeout")

		case <-vsockSrv.Done():
			log.Info("guest signaled done")
			return nil
		}
	}
}

// teardown cleans up after an execution.
func (r *Runner) teardown(ctx context.Context, log *slog.Logger, claimID, execID string) {
	log.Info("tearing down execution")

	// Teardown TAP.
	if err := execnet.Teardown(r.netCfg); err != nil {
		log.Warn("TAP teardown error", "error", err)
	}

	// Clean work directory.
	workDir := filepath.Join(config.WorkloadDir, execID)
	if err := os.RemoveAll(workDir); err != nil {
		log.Warn("work dir cleanup error", "error", err)
	}

	// Release lease (best effort).
	if err := r.lease.Release(ctx, claimID); err != nil {
		log.Warn("lease release failed", "error", err)
	}
}

// guestFiles returns the files to inject into the guest filesystem.
func (r *Runner) guestFiles() []*initpb.FileConfig {
	var files []*initpb.FileConfig

	// /etc/resolv.conf — copy from the pod's own DNS config.
	if data, err := os.ReadFile("/etc/resolv.conf"); err == nil {
		files = append(files, &initpb.FileConfig{
			Path:    "/etc/resolv.conf",
			Content: data,
			Mode:    0644,
		})
	}

	// /etc/hosts — minimal.
	files = append(files, &initpb.FileConfig{
		Path:    "/etc/hosts",
		Content: []byte("127.0.0.1 localhost\n::1 localhost\n"),
		Mode:    0644,
	})

	return files
}

// parseMemoryMB parses a memory string like "256M" or "1G" into megabytes.
func parseMemoryMB(s string) int {
	if len(s) == 0 {
		return 256
	}
	var n int
	unit := s[len(s)-1]
	fmt.Sscanf(s[:len(s)-1], "%d", &n)
	switch unit {
	case 'G', 'g':
		return n * 1024
	default:
		return n
	}
}
