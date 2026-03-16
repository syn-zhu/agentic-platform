//go:build linux

package proxy

import (
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"os"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
)

// OrigDest holds the original destination that was rewritten by the
// eBPF connect4 program. Layout matches struct orig_dest in connect4.c.
type OrigDest struct {
	IP4  uint32
	Port uint16
	_pad uint16
}

// Addr returns the original destination as "ip:port".
func (o OrigDest) Addr() string {
	ip := make(net.IP, 4)
	binary.BigEndian.PutUint32(ip, o.IP4)
	return fmt.Sprintf("%s:%d", ip, ntohs(o.Port))
}

// EBPFInterceptor manages the eBPF connect4 program lifecycle.
type EBPFInterceptor struct {
	collection *ebpf.Collection
	link       link.Link
	origDests  *ebpf.Map
}

// LoadEBPF loads the connect4 eBPF program from the embedded object file
// and attaches it to the given cgroup path.
func LoadEBPF(cgroupPath string, bpfObjPath string) (*EBPFInterceptor, error) {
	slog.Info("loading eBPF connect4 program", "cgroup", cgroupPath, "obj", bpfObjPath)

	spec, err := ebpf.LoadCollectionSpec(bpfObjPath)
	if err != nil {
		return nil, fmt.Errorf("load BPF spec: %w", err)
	}

	coll, err := ebpf.NewCollection(spec)
	if err != nil {
		return nil, fmt.Errorf("create BPF collection: %w", err)
	}

	prog := coll.Programs["intercept_connect"]
	if prog == nil {
		coll.Close()
		return nil, fmt.Errorf("program 'intercept_connect' not found in BPF object")
	}

	// Open the cgroup directory for attachment.
	cgroupFd, err := os.Open(cgroupPath)
	if err != nil {
		coll.Close()
		return nil, fmt.Errorf("open cgroup %s: %w", cgroupPath, err)
	}
	defer cgroupFd.Close()

	l, err := link.AttachCgroup(link.CgroupOptions{
		Path:    cgroupPath,
		Program: prog,
		Attach:  ebpf.AttachCGroupInet4Connect,
	})
	if err != nil {
		coll.Close()
		return nil, fmt.Errorf("attach to cgroup: %w", err)
	}

	origDests := coll.Maps["orig_dest_map"]
	if origDests == nil {
		l.Close()
		coll.Close()
		return nil, fmt.Errorf("map 'orig_dest_map' not found in BPF object")
	}

	slog.Info("eBPF connect4 attached", "cgroup", cgroupPath)

	return &EBPFInterceptor{
		collection: coll,
		link:       l,
		origDests:  origDests,
	}, nil
}

// LookupOrigDest reads the original destination for a redirected connection,
// identified by the socket cookie. Returns false if no entry exists.
func (e *EBPFInterceptor) LookupOrigDest(cookie uint64) (OrigDest, bool) {
	var dest OrigDest
	err := e.origDests.Lookup(cookie, &dest)
	if err != nil {
		return OrigDest{}, false
	}
	// Clean up the entry after reading.
	e.origDests.Delete(cookie)
	return dest, true
}

// Close detaches the eBPF program and frees resources.
func (e *EBPFInterceptor) Close() {
	slog.Info("closing eBPF interceptor")
	if e.link != nil {
		e.link.Close()
	}
	if e.collection != nil {
		e.collection.Close()
	}
}

// ntohs converts network byte order (big-endian) uint16 to host byte order.
func ntohs(n uint16) uint16 {
	b := [2]byte{}
	binary.BigEndian.PutUint16(b[:], n)
	return binary.LittleEndian.Uint16(b[:])
}
