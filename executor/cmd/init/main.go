//go:build linux

package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"syscall"
	"time"
	"unsafe"

	"github.com/containerd/ttrpc"
	"github.com/mdlayher/vsock"
	"github.com/vishvananda/netlink"
	"github.com/siyanzhu/agentic-platform/executor/internal/image"
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

	// 2. Dial host over vsock to get config.
	conn, err := dialHost(ctx)
	if err != nil {
		fatal("dial host: %v", err)
	}
	log.Println("init: connected to host via vsock")

	ttrpcClient := ttrpc.NewClient(conn)
	initClient := initpb.NewInitControlClient(ttrpcClient)

	resp, err := initClient.Init(ctx, &initpb.InitRequest{})
	if err != nil {
		fatal("Init RPC: %v", err)
	}
	ttrpcClient.Close()
	log.Println("init: got config from host")

	// 3. Wait for rootfs block device.
	waitForDevice("/dev/vda", 5*time.Second)

	// 4. Mount rootfs as overlayfs (read-only lower + tmpfs upper).
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

	// 5. Configure network using netlink.
	if resp.Network != nil {
		if err := configureNetwork(resp.Network); err != nil {
			fatal("configure network: %v", err)
		}
		log.Println("init: network configured")
	}

	// 6. Write injected files into the merged rootfs.
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

	// 7. Switch root to the merged overlay.
	if err := switchRoot("/mnt/merged"); err != nil {
		fatal("switch root: %v", err)
	}
	log.Println("init: switched root")

	// 8. Read image config and exec into agent.
	imgCfg, err := image.LoadConfig("/etc")
	if err != nil {
		fatal("load image config: %v", err)
	}
	log.Printf("init: exec-ing into agent: %v", imgCfg.Entrypoint)

	agentEnv := os.Environ()
	agentEnv = append(agentEnv, fmt.Sprintf("PORT=%d", imgCfg.Port))
	for k, v := range imgCfg.Env {
		agentEnv = append(agentEnv, fmt.Sprintf("%s=%s", k, v))
	}

	// Exec replaces this process with the agent. The agent becomes PID 1.
	if err := syscall.Exec(imgCfg.Entrypoint[0], imgCfg.Entrypoint, agentEnv); err != nil {
		fatal("exec agent: %v", err)
	}
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
func configureNetwork(cfg *initpb.NetworkConfig) error {
	link, err := netlink.LinkByName("eth0")
	if err != nil {
		return fmt.Errorf("find eth0: %w", err)
	}

	lo, err := netlink.LinkByName("lo")
	if err == nil {
		netlink.LinkSetUp(lo)
	}

	addr, err := netlink.ParseAddr(fmt.Sprintf("%s/%d", cfg.Ip, cfg.PrefixLen))
	if err != nil {
		return fmt.Errorf("parse addr: %w", err)
	}
	if err := netlink.AddrAdd(link, addr); err != nil {
		return fmt.Errorf("add addr: %w", err)
	}

	if cfg.Mtu > 0 {
		if err := netlink.LinkSetMTU(link, int(cfg.Mtu)); err != nil {
			return fmt.Errorf("set MTU: %w", err)
		}
	}

	if err := netlink.LinkSetUp(link); err != nil {
		return fmt.Errorf("link up: %w", err)
	}

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
	*(*int)(unsafe.Pointer(uintptr(0))) = 0
}
