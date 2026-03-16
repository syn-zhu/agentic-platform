//go:build linux

package pasta

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// Config holds pasta process configuration.
type Config struct {
	// AgentPort is the port to forward from the root netns into the VM.
	// The executor connects to localhost:AgentPort to talk to the agent.
	AgentPort int

	// PastaPath is the path to the pasta binary.
	PastaPath string
}

// DefaultConfig returns a Config with standard defaults.
func DefaultConfig(agentPort int) *Config {
	return &Config{
		AgentPort: agentPort,
		PastaPath: findPasta(),
	}
}

// Instance represents a running pasta process with its dedicated netns.
type Instance struct {
	cmd    *exec.Cmd
	nsPath string // Path to the network namespace (e.g., /proc/<pid>/ns/net)
}

// Start launches the pasta process. It creates a dedicated network namespace
// with a TAP device and sets up L2↔L4 translation between the namespace
// and the pod's root netns. Port forwarding is configured so the executor
// can reach the agent on localhost:AgentPort.
//
// The returned Instance holds the pasta process; call Stop() to clean up.
func Start(cfg *Config) (*Instance, error) {
	slog.Info("starting pasta", "agent_port", cfg.AgentPort, "binary", cfg.PastaPath)

	// Build pasta command line.
	// Key flags:
	//   --tcp-ports <port>  Forward this TCP port from root netns into the pasta netns
	//   --udp-ports <port>  Forward this UDP port (we only need TCP for the agent)
	//   --config-net        Let pasta auto-configure the netns networking
	//   --ns-ifname eth0    Name the TAP interface eth0 inside the netns (Firecracker expects this)
	args := []string{
		"--tcp-ports", strconv.Itoa(cfg.AgentPort),
		"--config-net",
		"--ns-ifname", "eth0",
	}

	cmd := exec.Command(cfg.PastaPath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// pasta needs to create a network namespace (CLONE_NEWNET).
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWNET,
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start pasta: %w", err)
	}

	pid := cmd.Process.Pid
	nsPath := fmt.Sprintf("/proc/%d/ns/net", pid)

	slog.Info("pasta started", "pid", pid, "ns", nsPath)

	return &Instance{
		cmd:    cmd,
		nsPath: nsPath,
	}, nil
}

// NsPath returns the path to the network namespace created by pasta.
// Pass this to Firecracker's jailer so the VM runs in the pasta netns.
func (i *Instance) NsPath() string {
	return i.nsPath
}

// Pid returns the pasta process PID.
func (i *Instance) Pid() int {
	return i.cmd.Process.Pid
}

// Stop kills the pasta process and cleans up.
func (i *Instance) Stop() error {
	slog.Info("stopping pasta", "pid", i.cmd.Process.Pid)
	if err := i.cmd.Process.Kill(); err != nil {
		return fmt.Errorf("kill pasta: %w", err)
	}
	// Wait to reap the process.
	i.cmd.Wait()
	return nil
}

// IsRunning checks if the pasta process is still alive.
func (i *Instance) IsRunning() bool {
	return i.cmd.Process.Signal(syscall.Signal(0)) == nil
}

// GuestNetConfig returns the network configuration for the guest.
// pasta auto-configures the netns with the pod's IP, gateway, and MTU.
// The guest init reads these from vsock Init and configures eth0.
func GuestNetConfig() (ip, gateway string, prefixLen, mtu int) {
	// pasta mirrors the pod's network config into the dedicated netns.
	// The guest gets the same IP, gateway, and MTU as the pod.
	// We discover these from the pod's own network.
	ip, gateway, prefixLen, mtu = discoverPodNetwork()
	return
}

// discoverPodNetwork reads the pod's IP, gateway, and MTU from the
// default-route interface.
func discoverPodNetwork() (ip, gateway string, prefixLen, mtu int) {
	// Read default route to find gateway and interface.
	data, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return "169.254.1.2", "169.254.1.1", 32, 1500 // fallback
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines[1:] { // skip header
		fields := strings.Fields(line)
		if len(fields) < 8 {
			continue
		}
		// Default route has destination 00000000.
		if fields[1] == "00000000" {
			ifName := fields[0]
			gw := parseHexIP(fields[2])
			// Read MTU from the interface.
			mtuData, err := os.ReadFile(fmt.Sprintf("/sys/class/net/%s/mtu", ifName))
			if err == nil {
				mtu, _ = strconv.Atoi(strings.TrimSpace(string(mtuData)))
			}
			if mtu == 0 {
				mtu = 1500
			}
			// Read IP from the interface.
			addrData, err := os.ReadFile(fmt.Sprintf("/sys/class/net/%s/address", ifName))
			_ = addrData
			// For pasta, the guest gets the pod's IP. Read it from the interface.
			ip = readInterfaceIP(ifName)
			gateway = gw
			prefixLen = 24 // Common default; pasta handles this.
			return
		}
	}
	return "169.254.1.2", "169.254.1.1", 32, 1500
}

func parseHexIP(hex string) string {
	if len(hex) != 8 {
		return ""
	}
	b := make([]byte, 4)
	for i := 0; i < 4; i++ {
		val, _ := strconv.ParseUint(hex[i*2:i*2+2], 16, 8)
		b[i] = byte(val)
	}
	// /proc/net/route uses little-endian.
	return fmt.Sprintf("%d.%d.%d.%d", b[3], b[2], b[1], b[0])
}

func readInterfaceIP(ifName string) string {
	// Read from /proc/net/fib_trie or use a simpler approach.
	// For now, read from ip command output.
	out, err := exec.Command("ip", "-4", "-o", "addr", "show", ifName).Output()
	if err != nil {
		return "169.254.1.2"
	}
	// Output: "2: eth0    inet 192.168.1.5/24 brd ..."
	fields := strings.Fields(string(out))
	for i, f := range fields {
		if f == "inet" && i+1 < len(fields) {
			parts := strings.Split(fields[i+1], "/")
			return parts[0]
		}
	}
	return "169.254.1.2"
}

func findPasta() string {
	for _, p := range []string{"/usr/bin/pasta", "/usr/local/bin/pasta"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "pasta" // rely on PATH
}
