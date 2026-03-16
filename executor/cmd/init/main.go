//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"syscall"
	"time"
	"unsafe"

	"github.com/containerd/ttrpc"
	"github.com/mdlayher/vsock"
	"github.com/vishvananda/netlink"
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
	log.Printf("init: got config (payload=%d bytes)", len(resp.Payload))

	// 4. Wait for rootfs block device.
	waitForDevice("/dev/vda", 5*time.Second)

	// 5. Mount rootfs as overlayfs (read-only lower + tmpfs upper).
	mustMkdir("/mnt", 0755)
	mustMkdir("/mnt/lower", 0755)
	mustMount("/dev/vda", "/mnt/lower", "ext4", unix.MS_RDONLY, "")
	mustMount("tmpfs", "/mnt/upper", "tmpfs", 0, "size=128M")
	mustMkdir("/mnt/upper/upper", 0755)
	mustMkdir("/mnt/upper/work", 0755)
	mustMkdir("/mnt/merged", 0755)
	mustMount("overlay", "/mnt/merged", "overlay", 0,
		"lowerdir=/mnt/lower,upperdir=/mnt/upper/upper,workdir=/mnt/upper/work")
	log.Println("init: overlayfs mounted")

	// 6. Configure network using netlink (no shell-out to ip command).
	if resp.Network != nil {
		if err := configureNetwork(resp.Network); err != nil {
			fatal("configure network: %v", err)
		}
		log.Println("init: network configured")
	}

	// 7. Write injected files into the merged rootfs.
	for _, f := range resp.Files {
		path := "/mnt/merged" + f.Path
		dir := path
		for i := len(path) - 1; i >= 0; i-- {
			if path[i] == '/' {
				dir = path[:i]
				break
			}
		}
		os.MkdirAll(dir, 0755)
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

	// 9. Read image config from rootfs.
	imgCfg, err := loadImageConfig("/etc/image-config.json")
	if err != nil {
		fatal("load image config: %v", err)
	}
	log.Printf("init: image config: entrypoint=%v port=%d", imgCfg.Entrypoint, imgCfg.Port)

	// 10. Start the agent process.
	log.Printf("init: starting agent: %v", imgCfg.Entrypoint)

	agentEnv := append(os.Environ(), fmt.Sprintf("PORT=%d", imgCfg.Port))
	for k, v := range imgCfg.Env {
		agentEnv = append(agentEnv, fmt.Sprintf("%s=%s", k, v))
	}

	agent := &syscall.ProcAttr{
		Env:   agentEnv,
		Files: []uintptr{os.Stdin.Fd(), os.Stdout.Fd(), os.Stderr.Fd()},
		Sys:   &syscall.SysProcAttr{Setpgid: true},
	}
	agentPid, err := syscall.ForkExec(imgCfg.Entrypoint[0], imgCfg.Entrypoint, agent)
	if err != nil {
		fatal("start agent: %v", err)
	}
	log.Printf("init: agent started (pid=%d)", agentPid)

	// 10. Wait for agent to be ready (poll the health endpoint).
	agentAddr := fmt.Sprintf("127.0.0.1:%d", imgCfg.Port)
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
		initClient.EmitEvent(ctx, &initpb.EmitEventRequest{
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
				if _, err := initClient.EmitEvent(ctx, &initpb.EmitEventRequest{Data: chunk}); err != nil {
					log.Printf("init: Stream RPC error: %v", err)
					break
				}
			}
			if readErr != nil {
				if readErr != io.EOF {
					log.Printf("init: agent read error: %v", readErr)
					initClient.EmitEvent(ctx, &initpb.EmitEventRequest{
						Done:  true,
						Error: readErr.Error(),
					})
				} else {
					initClient.EmitEvent(ctx, &initpb.EmitEventRequest{Done: true})
				}
				break
			}
		}
	}
	log.Println("init: stream complete")

shutdown:
	// 13. Kill agent and reboot.
	syscall.Kill(-agentPid, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		var ws syscall.WaitStatus
		syscall.Wait4(agentPid, &ws, 0, nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		syscall.Kill(-agentPid, syscall.SIGKILL)
		<-done
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

// configureNetwork sets up eth0 using netlink.
// Uses RTNH_F_ONLINK because the gateway is outside the /32 prefix.
func configureNetwork(cfg *initpb.NetworkConfig) error {
	// Find eth0.
	link, err := netlink.LinkByName("eth0")
	if err != nil {
		return fmt.Errorf("find eth0: %w", err)
	}

	// Bring up loopback.
	lo, err := netlink.LinkByName("lo")
	if err == nil {
		netlink.LinkSetUp(lo)
	}

	// Add IP address.
	addr, err := netlink.ParseAddr(fmt.Sprintf("%s/%d", cfg.Ip, cfg.PrefixLen))
	if err != nil {
		return fmt.Errorf("parse addr: %w", err)
	}
	if err := netlink.AddrAdd(link, addr); err != nil {
		return fmt.Errorf("add addr: %w", err)
	}

	// Set MTU.
	if cfg.Mtu > 0 {
		if err := netlink.LinkSetMTU(link, int(cfg.Mtu)); err != nil {
			return fmt.Errorf("set MTU: %w", err)
		}
	}

	// Bring up eth0.
	if err := netlink.LinkSetUp(link); err != nil {
		return fmt.Errorf("link up: %w", err)
	}

	// Add default route with ONLINK flag.
	// The gateway (169.254.1.1) is outside the /32 prefix, so the kernel
	// needs RTNH_F_ONLINK to accept the route without a connected route
	// to the gateway.
	gw := net.ParseIP(cfg.Gateway)
	if gw == nil {
		return fmt.Errorf("invalid gateway: %s", cfg.Gateway)
	}
	route := &netlink.Route{
		Gw:        gw,
		LinkIndex: link.Attrs().Index,
		Flags:     int(unix.RTNH_F_ONLINK),
	}
	if err := netlink.RouteAdd(route); err != nil {
		return fmt.Errorf("add default route: %w", err)
	}

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
	oldRoot := newRoot + "/old_root"
	os.MkdirAll(oldRoot, 0755)

	if err := unix.PivotRoot(newRoot, oldRoot); err != nil {
		return fmt.Errorf("pivot_root: %w", err)
	}
	if err := unix.Chdir("/"); err != nil {
		return fmt.Errorf("chdir: %w", err)
	}
	if err := unix.Unmount("/old_root", unix.MNT_DETACH); err != nil {
		return fmt.Errorf("unmount old_root: %w", err)
	}
	os.RemoveAll("/old_root")
	return nil
}

// imageConfig is read from /etc/image-config.json in the rootfs.
// It defines the agent entrypoint, port, and environment — the
// equivalent of OCI image config (ENTRYPOINT, EXPOSE, ENV).
type imageConfig struct {
	Entrypoint []string          `json:"entrypoint"`
	Port       int               `json:"port"`
	Env        map[string]string `json:"env"`
}

func loadImageConfig(path string) (*imageConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg imageConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(cfg.Entrypoint) == 0 {
		return nil, fmt.Errorf("entrypoint is required in %s", path)
	}
	if cfg.Port == 0 {
		cfg.Port = 8080
	}
	return &cfg, nil
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

func fatal(format string, args ...any) {
	log.Printf("init: FATAL: "+format, args...)
	// Trigger kernel panic → Firecracker exits.
	*(*int)(unsafe.Pointer(uintptr(0))) = 0
}
