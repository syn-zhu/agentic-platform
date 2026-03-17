//go:build linux

package pasta

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"

	"github.com/containers/common/libnetwork/pasta"
	"github.com/containers/common/libnetwork/types"
)

const (
	// ProxyPort is the port the eBPF proxy listens on.
	// Also forwarded by pasta so the agent can reach it.
	ProxyPort = 3128

	// CgroupName is the cgroup created for the pasta process.
	CgroupName = "pasta-proxy"
)

// Config holds pasta setup configuration.
type Config struct {
	// AgentPort is the TCP port to forward from the root netns into the pasta netns.
	AgentPort int

	// NsDir is the directory where the netns file is created.
	NsDir string
}

// Instance represents a running pasta setup with its dedicated netns.
type Instance struct {
	nsPath     string
	cgroupPath string
	result     *pasta.SetupResult
}

// Setup creates a network namespace with pasta, configures port forwarding,
// and creates a cgroup for eBPF attachment.
// pasta automatically exits when the netns path is deleted.
func Setup(cfg *Config) (*Instance, error) {
	slog.Info("setting up pasta", "agent_port", cfg.AgentPort)

	// Create a cgroup for the pasta process.
	// The eBPF connect4 program attaches to this cgroup to intercept
	// pasta's outbound connect() calls.
	cgroupPath := filepath.Join("/sys/fs/cgroup", CgroupName)
	if err := os.MkdirAll(cgroupPath, 0755); err != nil {
		return nil, fmt.Errorf("create cgroup %s: %w", cgroupPath, err)
	}

	// Move the current process into the cgroup temporarily so that
	// pasta (which forks from us) inherits it.
	// Save original cgroup to restore after.
	origCgroup, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return nil, fmt.Errorf("read current cgroup: %w", err)
	}

	if err := os.WriteFile(filepath.Join(cgroupPath, "cgroup.procs"), []byte("0"), 0644); err != nil {
		return nil, fmt.Errorf("move to pasta cgroup: %w", err)
	}

	// Create a netns path for pasta.
	if err := os.MkdirAll(cfg.NsDir, 0755); err != nil {
		return nil, fmt.Errorf("create ns dir: %w", err)
	}
	nsPath := filepath.Join(cfg.NsDir, "pasta-netns")

	f, err := os.Create(nsPath)
	if err != nil {
		return nil, fmt.Errorf("create netns file: %w", err)
	}
	f.Close()

	result, err := pasta.Setup(&pasta.SetupOptions{
		Netns: nsPath,
		Ports: []types.PortMapping{
			{
				ContainerPort: uint16(cfg.AgentPort),
				HostPort:      uint16(cfg.AgentPort),
				Protocol:      "tcp",
			},
		},
	})

	// Move ourselves back to the original cgroup regardless of pasta result.
	// Parse the original cgroup path from /proc/self/cgroup format "0::/path"
	restoreCgroup(origCgroup)

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

// NsPath returns the path to the network namespace.
func (i *Instance) NsPath() string {
	return i.nsPath
}

// CgroupPath returns the cgroup path for eBPF attachment.
func (i *Instance) CgroupPath() string {
	return i.cgroupPath
}

// IPAddresses returns the IP addresses configured by pasta.
func (i *Instance) IPAddresses() []net.IP {
	return i.result.IPAddresses
}

// Teardown removes the netns path (pasta exits automatically)
// and cleans up the cgroup.
func (i *Instance) Teardown() {
	slog.Info("tearing down pasta", "ns", i.nsPath)
	os.Remove(i.nsPath)
	os.Remove(i.cgroupPath)
}

// restoreCgroup moves the current process back to its original cgroup.
func restoreCgroup(origData []byte) {
	// /proc/self/cgroup format: "0::/path\n"
	// Extract the path after "::"
	s := string(origData)
	for i := 0; i < len(s); i++ {
		if i+2 < len(s) && s[i] == ':' && s[i+1] == ':' {
			path := s[i+2:]
			// Trim newline
			for len(path) > 0 && (path[len(path)-1] == '\n' || path[len(path)-1] == '\r') {
				path = path[:len(path)-1]
			}
			cgPath := filepath.Join("/sys/fs/cgroup", path, "cgroup.procs")
			os.WriteFile(cgPath, []byte("0"), 0644)
			return
		}
	}
}
