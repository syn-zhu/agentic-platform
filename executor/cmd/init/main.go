//go:build linux

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"syscall"
	"time"
	"unsafe"

	"github.com/containerd/ttrpc"
	"github.com/mdlayher/vsock"
	initpb "github.com/siyanzhu/agentic-platform/executor/internal/vsock/initpb"
	"golang.org/x/sys/unix"
)

const (
	hostCID     = 2     // Always the host in Firecracker vsock.
	controlPort = 10000 // vsock port for ttrpc.
)

func main() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.Println("init: starting")

	ctx := context.Background()

	// 1. Mount pseudo-filesystems.
	mustMount("proc", "/proc", "proc", 0, "")
	mustMount("sysfs", "/sys", "sysfs", 0, "")
	mustMount("devtmpfs", "/dev", "devtmpfs", 0, "")
	mustMount("devpts", "/dev/pts", "devpts", 0, "")
	log.Println("init: pseudo-filesystems mounted")

	// 2. Dial host over vsock.
	conn, err := dialHost(ctx)
	if err != nil {
		fatal("dial host: %v", err)
	}
	log.Println("init: connected to host via vsock")

	// 3. Create ttrpc client and call Init.
	ttrpcClient := ttrpc.NewClient(conn)
	initClient := initpb.NewInitControlClient(ttrpcClient)

	resp, err := initClient.Init(ctx, &initpb.InitRequest{})
	if err != nil {
		fatal("Init RPC: %v", err)
	}
	log.Printf("init: got config (agent_port=%d, payload=%d bytes)", resp.AgentPort, len(resp.Payload))

	// 4. Wait for rootfs block device.
	waitForDevice("/dev/vda", 5*time.Second)

	// 5. Mount rootfs as overlayfs (read-only lower + tmpfs upper).
	mustMkdir("/mnt", 0755)
	mustMount("/dev/vda", "/mnt/lower", "ext4", unix.MS_RDONLY, "")
	mustMount("tmpfs", "/mnt/upper", "tmpfs", 0, "size=128M")
	mustMkdir("/mnt/upper/upper", 0755)
	mustMkdir("/mnt/upper/work", 0755)
	mustMkdir("/mnt/merged", 0755)
	mustMount("overlay", "/mnt/merged", "overlay", 0,
		"lowerdir=/mnt/lower,upperdir=/mnt/upper/upper,workdir=/mnt/upper/work")
	log.Println("init: overlayfs mounted")

	// 6. Configure network.
	if resp.Network != nil {
		if err := configureNetwork(resp.Network); err != nil {
			fatal("configure network: %v", err)
		}
		log.Println("init: network configured")
	}

	// 7. Write injected files.
	for _, f := range resp.Files {
		path := "/mnt/merged" + f.Path
		mustMkdirAll(path)
		if err := os.WriteFile(path, f.Content, os.FileMode(f.Mode)); err != nil {
			fatal("write file %s: %v", f.Path, err)
		}
	}
	log.Println("init: files written")

	// 8. Switch root to the merged overlay.
	if err := switchRoot("/mnt/merged"); err != nil {
		fatal("switch root: %v", err)
	}
	log.Println("init: switched root")

	// 9. Start the agent process.
	// The agent's entrypoint is /entrypoint.sh or whatever the image defines.
	// For now we look for /entrypoint.sh, then fall back to /bin/sh.
	agentCmd := findEntrypoint()
	log.Printf("init: starting agent: %s", agentCmd)

	agent := exec.Command(agentCmd)
	agent.Stdout = os.Stdout
	agent.Stderr = os.Stderr
	agent.Env = append(os.Environ(),
		fmt.Sprintf("PORT=%d", resp.AgentPort),
	)
	agent.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := agent.Start(); err != nil {
		fatal("start agent: %v", err)
	}
	log.Printf("init: agent started (pid=%d)", agent.Process.Pid)

	// 10. Wait for agent to be ready (poll the health endpoint).
	agentAddr := fmt.Sprintf("127.0.0.1:%d", resp.AgentPort)
	if err := waitForAgent(ctx, agentAddr, 30*time.Second); err != nil {
		fatal("agent not ready: %v", err)
	}
	log.Println("init: agent is ready")

	// 11. Forward payload to agent.
	agentURL := fmt.Sprintf("http://%s/run", agentAddr)
	agentReq, err := http.NewRequestWithContext(ctx, http.MethodPost, agentURL, bytes.NewReader(resp.Payload))
	if err != nil {
		fatal("create agent request: %v", err)
	}
	for k, v := range resp.PayloadHeaders {
		agentReq.Header.Set(k, v)
	}

	agentResp, err := http.DefaultClient.Do(agentReq)
	if err != nil {
		// Report error to host via Stream.
		initClient.Stream(ctx, &initpb.StreamRequest{
			Done:  true,
			Error: fmt.Sprintf("agent request failed: %v", err),
		})
		goto shutdown
	}
	defer agentResp.Body.Close()

	log.Printf("init: agent responded with %d", agentResp.StatusCode)

	// 12. Stream agent response back to host via Stream RPC.
	{
		buf := make([]byte, 4096)
		for {
			n, readErr := agentResp.Body.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				if _, err := initClient.Stream(ctx, &initpb.StreamRequest{Data: chunk}); err != nil {
					log.Printf("init: Stream RPC error: %v", err)
					break
				}
			}
			if readErr != nil {
				if readErr != io.EOF {
					log.Printf("init: agent read error: %v", readErr)
					initClient.Stream(ctx, &initpb.StreamRequest{
						Done:  true,
						Error: readErr.Error(),
					})
				} else {
					initClient.Stream(ctx, &initpb.StreamRequest{Done: true})
				}
				break
			}
		}
	}
	log.Println("init: stream complete")

shutdown:
	// 13. Kill agent and reboot.
	if agent.Process != nil {
		syscall.Kill(-agent.Process.Pid, syscall.SIGTERM)
		done := make(chan error, 1)
		go func() { done <- agent.Wait() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			syscall.Kill(-agent.Process.Pid, syscall.SIGKILL)
			<-done
		}
	}

	ttrpcClient.Close()
	log.Println("init: rebooting")
	unix.Sync()
	unix.Reboot(unix.LINUX_REBOOT_CMD_RESTART)
}

// dialHost connects to the executor over vsock with retries.
func dialHost(ctx context.Context) (net.Conn, error) {
	for i := 0; i < 200; i++ {
		conn, err := vsock.Dial(hostCID, controlPort, nil)
		if err == nil {
			return conn, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil, fmt.Errorf("vsock dial timed out after 2s")
}

// configureNetwork sets up eth0 with the given config.
// Uses RTNH_F_ONLINK because the gateway is outside the /32 prefix.
func configureNetwork(cfg *initpb.NetworkConfig) error {
	iface, err := net.InterfaceByName("eth0")
	if err != nil {
		return fmt.Errorf("find eth0: %w", err)
	}

	// Parse IP.
	ip := net.ParseIP(cfg.Ip)
	if ip == nil {
		return fmt.Errorf("invalid IP: %s", cfg.Ip)
	}

	// Add address using ioctl (netlink requires more deps in the guest).
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, 0)
	if err != nil {
		return fmt.Errorf("socket: %w", err)
	}
	defer unix.Close(fd)

	// Use ip command for simplicity in the guest (available in most rootfs).
	// This avoids pulling netlink into the static init binary.
	if err := run("ip", "addr", "add", fmt.Sprintf("%s/%d", cfg.Ip, cfg.PrefixLen), "dev", "eth0"); err != nil {
		return fmt.Errorf("ip addr add: %w", err)
	}
	if err := run("ip", "link", "set", "eth0", "up"); err != nil {
		return fmt.Errorf("ip link set up: %w", err)
	}
	if err := run("ip", "link", "set", "lo", "up"); err != nil {
		return fmt.Errorf("ip link set lo up: %w", err)
	}

	// Set MTU.
	if cfg.Mtu > 0 {
		if err := run("ip", "link", "set", "eth0", "mtu", fmt.Sprintf("%d", cfg.Mtu)); err != nil {
			return fmt.Errorf("ip link set mtu: %w", err)
		}
	}

	// Add default route with onlink flag (gateway is outside /32 prefix).
	if err := run("ip", "route", "add", "default", "via", cfg.Gateway, "onlink", "dev", "eth0"); err != nil {
		return fmt.Errorf("ip route add: %w", err)
	}

	_ = iface
	return nil
}

// waitForDevice polls for a block device to appear.
func waitForDevice(path string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	log.Printf("init: warning: device %s not found after %v", path, timeout)
}

// waitForAgent polls the agent's health endpoint until it responds.
func waitForAgent(ctx context.Context, addr string, timeout time.Duration) error {
	client := &http.Client{Timeout: 1 * time.Second}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.Get(fmt.Sprintf("http://%s/healthz", addr))
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("agent at %s not ready after %v", addr, timeout)
}

// switchRoot performs a pivot_root into the new root.
func switchRoot(newRoot string) error {
	// Create the old_root mount point.
	oldRoot := newRoot + "/old_root"
	mustMkdir(oldRoot, 0755)

	// pivot_root.
	if err := unix.PivotRoot(newRoot, oldRoot); err != nil {
		return fmt.Errorf("pivot_root: %w", err)
	}

	// Change to the new root.
	if err := unix.Chdir("/"); err != nil {
		return fmt.Errorf("chdir: %w", err)
	}

	// Unmount old root.
	if err := unix.Unmount("/old_root", unix.MNT_DETACH); err != nil {
		return fmt.Errorf("unmount old_root: %w", err)
	}
	os.RemoveAll("/old_root")

	return nil
}

// findEntrypoint looks for common agent entrypoints.
func findEntrypoint() string {
	candidates := []string{"/entrypoint.sh", "/app/entrypoint.sh", "/agent/entrypoint.sh"}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return "/bin/sh"
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func mustMount(source, target, fstype string, flags uintptr, data string) {
	os.MkdirAll(target, 0755)
	if err := unix.Mount(source, target, fstype, flags, data); err != nil {
		fatal("mount %s on %s (%s): %v", source, target, fstype, err)
	}
}

func mustMkdir(path string, perm os.FileMode) {
	os.MkdirAll(path, perm)
}

func mustMkdirAll(path string) {
	dir := path[:len(path)-len(path[len(path)-1:])]
	// Extract directory from file path.
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			dir = path[:i]
			break
		}
	}
	os.MkdirAll(dir, 0755)
}

func fatal(format string, args ...any) {
	log.Printf("init: FATAL: "+format, args...)
	// Trigger kernel panic → Firecracker exits.
	*(*int)(unsafe.Pointer(uintptr(0))) = 0
}
