//go:build linux

package vm

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	firecracker "github.com/firecracker-microvm/firecracker-go-sdk"
	"github.com/firecracker-microvm/firecracker-go-sdk/client/models"
)

const (
	// GuestCID is the vsock CID assigned to the guest. Always 3 in Firecracker.
	GuestCID = 3
)

// Config holds VM configuration.
type Config struct {
	KernelPath  string
	InitrdPath  string // Path to initramfs (cpio+lz4 with our init binary)
	RootfsPath  string
	TAPName     string
	VCPUs       int
	MemoryMB    int
	WorkDir     string // Per-execution working directory
	VsockPath   string // Host-side Unix socket path (created by vsock server before boot)
	NsPath      string // Network namespace path (from pasta) — Firecracker runs in this netns
}

// VM wraps a Firecracker machine instance.
type VM struct {
	machine *firecracker.Machine
}

// Boot creates and starts a Firecracker VM.
func Boot(ctx context.Context, cfg Config) (*VM, error) {
	slog.Info("booting VM",
		"kernel", cfg.KernelPath,
		"initrd", cfg.InitrdPath,
		"rootfs", cfg.RootfsPath,
		"vcpus", cfg.VCPUs,
		"memory_mb", cfg.MemoryMB,
		"tap", cfg.TAPName,
		"netns", cfg.NsPath,
	)

	socketPath := filepath.Join(cfg.WorkDir, "firecracker.sock")

	vcpuCount := int64(cfg.VCPUs)
	memSize := int64(cfg.MemoryMB)
	smt := false

	fcCfg := firecracker.Config{
		SocketPath:      socketPath,
		KernelImagePath: cfg.KernelPath,
		InitrdPath:      cfg.InitrdPath,
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
				Path: cfg.VsockPath,
				CID:  GuestCID,
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
	return &VM{machine: m}, nil
}

// Wait blocks until the VM exits.
func (v *VM) Wait(ctx context.Context) error {
	return v.machine.Wait(ctx)
}

// Pause freezes the VM's vCPUs. Memory is retained. The VM can be
// resumed with Unpause. While paused, the VM uses zero CPU but its
// memory footprint remains allocated.
func (v *VM) Pause(ctx context.Context) error {
	slog.Info("pausing VM")
	return v.machine.PauseVM(ctx)
}

// Unpause resumes a paused VM. The agent process continues from
// where it was paused — in-memory state is intact.
func (v *VM) Unpause(ctx context.Context) error {
	slog.Info("unpausing VM")
	return v.machine.ResumeVM(ctx)
}

// Stop kills the Firecracker VMM process.
func (v *VM) Stop() {
	slog.Info("stopping VM")
	v.machine.StopVMM()
}
