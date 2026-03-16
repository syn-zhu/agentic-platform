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

// Config holds pasta setup configuration.
type Config struct {
	// AgentPort is the TCP port to forward from the root netns into the pasta netns.
	AgentPort int

	// NsDir is the directory where the netns file is created.
	NsDir string
}

// Instance represents a running pasta setup with its dedicated netns.
type Instance struct {
	nsPath string
	result *pasta.SetupResult
}

// Setup creates a network namespace with pasta and configures port forwarding.
// pasta automatically exits when the netns path is deleted.
func Setup(cfg *Config) (*Instance, error) {
	slog.Info("setting up pasta", "agent_port", cfg.AgentPort)

	// Create a netns path for pasta.
	if err := os.MkdirAll(cfg.NsDir, 0755); err != nil {
		return nil, fmt.Errorf("create ns dir: %w", err)
	}
	nsPath := filepath.Join(cfg.NsDir, "pasta-netns")

	// Create the netns file (pasta expects it to exist).
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
	if err != nil {
		os.Remove(nsPath)
		return nil, fmt.Errorf("pasta setup: %w", err)
	}

	slog.Info("pasta setup complete",
		"ns", nsPath,
		"ipv6", result.IPv6,
	)

	return &Instance{
		nsPath: nsPath,
		result: result,
	}, nil
}

// NsPath returns the path to the network namespace.
func (i *Instance) NsPath() string {
	return i.nsPath
}

// IPAddresses returns the IP addresses configured by pasta.
func (i *Instance) IPAddresses() []net.IP {
	return i.result.IPAddresses
}

// Teardown removes the netns path, which causes pasta to exit automatically.
func (i *Instance) Teardown() {
	slog.Info("tearing down pasta", "ns", i.nsPath)
	os.Remove(i.nsPath)
}