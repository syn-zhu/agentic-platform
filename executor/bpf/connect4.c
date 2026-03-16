// SPDX-License-Identifier: GPL-2.0
//
// connect4.c — eBPF cgroup/connect4 program for transparent HTTP interception.
//
// Attached to pasta's cgroup. Intercepts TCP connect() syscalls to ports
// 80 and 443, saves the original destination to a BPF map, and rewrites
// the destination to a local proxy (127.0.0.1:3128).
//
// The proxy reads the original destination from the BPF map using the
// socket cookie as the key, then forwards the request to the real
// destination (through ztunnel/waypoint).
//
// Compiled via cilium/ebpf's bpf2go:
//   //go:generate go run github.com/cilium/ebpf/cmd/bpf2go -target amd64 connect4 connect4.c

#include <linux/bpf.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>

#define PROXY_PORT 3128

struct orig_dest {
	__u32 ip4;
	__u16 port;
	__u16 _pad;
};

// Map: socket cookie → original destination.
// The proxy looks up entries after accepting a redirected connection.
struct {
	__uint(type, BPF_MAP_TYPE_HASH);
	__uint(max_entries, 4096);
	__type(key, __u64);
	__type(value, struct orig_dest);
} orig_dest_map SEC(".maps");

SEC("cgroup/connect4")
int intercept_connect(struct bpf_sock_addr *ctx)
{
	__u16 dst_port = bpf_ntohs(ctx->user_port);

	// Only intercept HTTP (80) and HTTPS (443).
	if (dst_port != 80 && dst_port != 443)
		return 1;

	// Save original destination keyed by socket cookie.
	__u64 cookie = bpf_get_socket_cookie(ctx);
	struct orig_dest dest = {
		.ip4 = ctx->user_ip4,
		.port = ctx->user_port,
	};
	bpf_map_update_elem(&orig_dest_map, &cookie, &dest, BPF_ANY);

	// Rewrite destination to local proxy.
	ctx->user_ip4 = bpf_htonl(0x7f000001); // 127.0.0.1
	ctx->user_port = bpf_htons(PROXY_PORT);

	return 1;
}

char LICENSE[] SEC("license") = "GPL";
