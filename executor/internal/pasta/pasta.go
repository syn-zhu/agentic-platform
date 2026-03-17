//go:build linux

package pasta

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	// ProxyPort is the port the eBPF proxy listens on.
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
	ips        []net.IP
}

// Setup creates a network namespace with pasta, configures port forwarding,
// and starts pasta in a dedicated cgroup for eBPF attachment.
// pasta forks a daemon and exits — the daemon runs in the cgroup and
// automatically exits when the netns path is deleted.
func Setup(cfg *Config) (*Instance, error) {
	slog.Info("setting up pasta", "agent_port", cfg.AgentPort)

	pastaPath, err := findPasta()
	if err != nil {
		return nil, err
	}

	// Create cgroup for the pasta daemon.
	cgroupPath := filepath.Join("/sys/fs/cgroup", CgroupName)
	if err := os.MkdirAll(cgroupPath, 0755); err != nil {
		return nil, fmt.Errorf("create cgroup %s: %w", cgroupPath, err)
	}

	// Open the cgroup directory fd for UseCgroupFD.
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

	// Build pasta command args.
	args := []string{
		"--config-net",
		"--quiet",
		"--no-map-gw",
		"-t", fmt.Sprintf("%d-%d:%d-%d", cfg.AgentPort, cfg.AgentPort, cfg.AgentPort, cfg.AgentPort),
		"-T", "none",
		"-u", "none",
		"-U", "none",
		"--dns-forward", "169.254.1.1",
		"--map-guest-addr", "169.254.1.2",
		"--netns", nsPath,
	}

	// Start pasta in the dedicated cgroup.
	// pasta forks a daemon — the child inherits the cgroup.
	cmd := exec.Command(pastaPath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		UseCgroupFD: true,
		CgroupFD:    cgroupFd,
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		os.Remove(nsPath)
		return nil, fmt.Errorf("pasta failed: %w\noutput: %s", err, string(out))
	}

	if len(out) > 0 {
		slog.Info("pasta output", "msg", strings.TrimSpace(string(out)))
	}

	// Discover IPs configured by pasta in the netns.
	ips, err := discoverNetnsIPs(nsPath)
	if err != nil {
		slog.Warn("failed to discover pasta IPs", "error", err)
	}

	slog.Info("pasta setup complete",
		"ns", nsPath,
		"cgroup", cgroupPath,
		"ips", ips,
	)

	return &Instance{
		nsPath:     nsPath,
		cgroupPath: cgroupPath,
		ips:        ips,
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
	return i.ips
}

// Teardown removes the netns path (pasta exits automatically)
// and cleans up the cgroup.
func (i *Instance) Teardown() {
	slog.Info("tearing down pasta", "ns", i.nsPath)
	os.Remove(i.nsPath)
	os.Remove(i.cgroupPath)
}

// findPasta locates the pasta binary.
func findPasta() (string, error) {
	for _, p := range []string{"/usr/bin/pasta", "/usr/local/bin/pasta"} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	path, err := exec.LookPath("pasta")
	if err != nil {
		return "", fmt.Errorf("pasta binary not found: %w", err)
	}
	return path, nil
}

// discoverNetnsIPs reads IP addresses from the pasta netns.
func discoverNetnsIPs(nsPath string) ([]net.IP, error) {
	// Use ip command to list addresses in the netns.
	out, err := exec.Command("ip", "-n", nsPath, "-o", "-4", "addr", "show").Output()
	if err != nil {
		return nil, err
	}

	var ips []net.IP
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		for i, f := range fields {
			if f == "inet" && i+1 < len(fields) {
				parts := strings.Split(fields[i+1], "/")
				if ip := net.ParseIP(parts[0]); ip != nil && !ip.IsLoopback() {
					ips = append(ips, ip)
				}
			}
		}
	}
	return ips, nil
}
