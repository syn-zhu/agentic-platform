//go:build linux

package pasta

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"syscall"

	upstream "github.com/siyanzhu/agentic-platform/executor/internal/pasta/upstream"
	"github.com/containers/common/libnetwork/types"
)

const (
	ProxyPort  = 3128
	CgroupName = "pasta-proxy"
)

type Config struct {
	AgentPort int
	NsDir     string
}

type Instance struct {
	nsPath     string
	cgroupPath string
	result     *upstream.SetupResult
}

func Setup(cfg *Config) (*Instance, error) {
	slog.Info("setting up pasta", "agent_port", cfg.AgentPort)

	// Create cgroup for eBPF attachment.
	cgroupPath := filepath.Join("/sys/fs/cgroup", CgroupName)
	if err := os.MkdirAll(cgroupPath, 0755); err != nil {
		return nil, fmt.Errorf("create cgroup %s: %w", cgroupPath, err)
	}

	// Open cgroup fd for UseCgroupFD.
	cgroupFd, err := syscall.Open(cgroupPath, syscall.O_RDONLY|syscall.O_DIRECTORY, 0)
	if err != nil {
		return nil, fmt.Errorf("open cgroup fd: %w", err)
	}
	defer syscall.Close(cgroupFd)

	// Create netns path.
	if err := os.MkdirAll(cfg.NsDir, 0755); err != nil {
		return nil, fmt.Errorf("create ns dir: %w", err)
	}
	nsPath := filepath.Join(cfg.NsDir, "pasta-netns")
	f, err := os.Create(nsPath)
	if err != nil {
		return nil, fmt.Errorf("create netns file: %w", err)
	}
	f.Close()

	// Start pasta in the cgroup via CgroupFD.
	result, err := upstream.Setup(&upstream.SetupOptions{
		Netns: nsPath,
		Ports: []types.PortMapping{
			{
				ContainerPort: uint16(cfg.AgentPort),
				HostPort:      uint16(cfg.AgentPort),
				Protocol:      "tcp",
			},
		},
		CgroupFD: cgroupFd,
	})
	if err != nil {
		os.Remove(nsPath)
		return nil, fmt.Errorf("pasta setup: %w", err)
	}

	slog.Info("pasta setup complete",
		"ns", nsPath,
		"cgroup", cgroupPath,
		"ipv6", result.IPv6,
	)

	return &Instance{
		nsPath:     nsPath,
		cgroupPath: cgroupPath,
		result:     result,
	}, nil
}

func (i *Instance) NsPath() string       { return i.nsPath }
func (i *Instance) CgroupPath() string    { return i.cgroupPath }
func (i *Instance) IPAddresses() []net.IP { return i.result.IPAddresses }

func (i *Instance) Teardown() {
	slog.Info("tearing down pasta", "ns", i.nsPath)
	os.Remove(i.nsPath)
	os.Remove(i.cgroupPath)
}
