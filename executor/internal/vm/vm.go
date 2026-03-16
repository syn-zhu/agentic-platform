//go:build linux

package vm

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	firecracker "github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/firecracker-microvm/firecracker-go-sdk/client/models"
)

// Config holds VM configuration.
type Config struct {
	KernelPath string
	RootfsPath string
	TAPName    string
	VCPUs      int
	MemoryMB   int
	WorkDir    string // Per-execution working directory
	VsockCID   uint32
}

// VsockPath returns the host-side Unix socket path for the vsock device.
func (c *Config) VsockPath() string {
	return filepath.Join(c.WorkDir, "vsock")
}

// VM wraps a Firecracker machine instance.
type VM struct {
	machine *firecracker.Machine
	cfg     Config
}

// Boot creates and starts a Firecracker VM.
func Boot(ctx context.Context, cfg Config) (*VM, error) {
	slog.Info("booting VM",
		"kernel", cfg.KernelPath,
		"rootfs", cfg.RootfsPath,
		"vcpus", cfg.VCPUs,
		"memory_mb", cfg.MemoryMB,
		"tap", cfg.TAPName,
	)

	if err := os.MkdirAll(cfg.WorkDir, 0755); err != nil {
		return nil, fmt.Errorf("create work dir: %w", err)
	}

	socketPath := filepath.Join(cfg.WorkDir, "firecracker.sock")

	vcpuCount := int64(cfg.VCPUs)
	memSize := int64(cfg.MemoryMB)
	smt := false

	fcCfg := firecracker.Config{
		SocketPath:      socketPath,
		KernelImagePath: cfg.KernelPath,
		KernelArgs:      "console=ttyS0 reboot=k panic=1 pci=off net.ifnames=0 rdinit=/init",
		Drives: []models.Drive{
			{
				DriveID:      firecracker.String("rootfs"),
				PathOnHost:   firecracker.String(cfg.RootfsPath),
				IsRootDevice: firecracker.Bool(true),
				IsReadOnly:   firecracker.Bool(true),
			},
		},
		MachineCfg: models.MachineConfiguration{
			VcpuCount:  &vcpuCount,
			MemSizeMib: &memSize,
			Smt:        &smt,
		},
		NetworkInterfaces: []firecracker.NetworkInterface{
			{
				StaticConfiguration: &firecracker.StaticNetworkConfiguration{
					HostDevName: cfg.TAPName,
				},
			},
		},
		VsockDevices: []firecracker.VsockDevice{
			{
				Path: cfg.VsockPath(),
				CID:  cfg.VsockCID,
			},
		},
	}

	m, err := firecracker.NewMachine(ctx, fcCfg)
	if err != nil {
		return nil, fmt.Errorf("create machine: %w", err)
	}

	if err := m.Start(ctx); err != nil {
		return nil, fmt.Errorf("start machine: %w", err)
	}

	slog.Info("VM started")
	return &VM{machine: m, cfg: cfg}, nil
}

// Wait blocks until the VM exits.
func (v *VM) Wait(ctx context.Context) error {
	return v.machine.Wait(ctx)
}

// Stop kills the Firecracker VMM process.
func (v *VM) Stop() {
	slog.Info("stopping VM")
	v.machine.StopVMM()
}

// Cleanup removes the per-execution working directory.
func (v *VM) Cleanup() error {
	return os.RemoveAll(v.cfg.WorkDir)
}
