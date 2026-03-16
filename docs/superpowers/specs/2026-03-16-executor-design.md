# Executor Design Spec

**Date:** 2026-03-16
**Status:** Draft (v3 — session affinity + warm VMs)
**Author:** Siyanzhu + Claude

## Problem

The current Magenta executor (`fctr`) uses `tc redirect` for VM networking, which bypasses L3/iptables entirely and is incompatible with both Istio ambient mesh (ztunnel) and the Tenant Egress Gateway. Its external API is ttrpc over a Unix socket, designed for one-shot execution, with no concept of warm pools, claim leases, or request proxying. It cannot integrate with the pool operator or the ambient mesh architecture.

We need an executor that:

1. Uses networking compatible with Istio ambient mesh (ztunnel captures agent outbound traffic)
2. Integrates with the pool operator (claim/renew/release lifecycle via gRPC)
3. Accepts HTTP requests from the waypoint and forwards them to the agent inside the VM
4. Runs as a long-lived process handling sequential requests (pool model, not one-shot)
5. Streams SSE responses from the agent back through the waypoint to the client

## Solution

A new executor binary that uses [pasta](https://passt.top/) for VM networking. pasta creates a dedicated network namespace with a TAP device and translates between L2 Ethernet frames (from the VM) and L4 socket operations (in the pod's root netns). This gives us two key properties:

1. **Agent outbound traffic appears as normal socket operations** in the pod's root netns — ztunnel captures them via the OUTPUT chain automatically, no annotations or iptables workarounds needed.
2. **The executor can talk to the agent directly via localhost** — pasta forwards a port range from the pod's root netns into the VM. The executor sends HTTP requests to `localhost:8080`, pasta bridges them to the agent inside the VM.

This eliminates the need for vsock-based payload relay — the executor talks HTTP to the agent directly, and the guest init simplifies to just system setup (mount rootfs, configure network, exec into agent).

## Architecture

```
┌──────────────────────────────────────────────────────────────┐
│  Executor Pod (c5.metal node, ambient mesh enrolled)          │
│                                                               │
│  Pod root netns                                               │
│  ┌──────────────────────────────────────────────────────────┐ │
│  │  Executor Process                                         │ │
│  │    HTTP Server (:9090) — /healthz, /run, /status          │ │
│  │    Lease Client — gRPC Renew/Release to pool operator     │ │
│  │    State Machine — IDLE → STARTING → RUNNING → TEARDOWN   │ │
│  │                                                           │ │
│  │  On /run:                                                 │ │
│  │    POST http://localhost:8080/run  ──┐                     │ │
│  │    (reads SSE stream back)          │                     │ │
│  │                                     │                     │ │
│  │  pasta process                      │ port forwarding     │ │
│  │    (L2 ↔ L4 translation)           │ localhost:8080       │ │
│  │                                     │                     │ │
│  │  eth0 (pod IP)                      │                     │ │
│  │    outbound from pasta  ───→ ztunnel OUTPUT chain capture │ │
│  └─────────────────────────────┼────────────────────────────┘ │
│                                │                              │
│  ─────────────────── netns boundary ─────────────────────     │
│                                │                              │
│  Dedicated netns (created by pasta)                           │
│  ┌─────────────────────────────┼────────────────────────────┐ │
│  │  TAP device ←───── pasta ───┘                             │ │
│  │                                                           │ │
│  │  Firecracker VM                                           │ │
│  │    Guest Init (PID 1)                                     │ │
│  │      Mounts rootfs, configures network, execs agent       │ │
│  │    Agent Process (:8080)                                  │ │
│  │      Handles requests, returns SSE streams                │ │
│  │      Outbound → TAP → pasta → root netns socket → ztunnel│ │
│  └───────────────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────────┘
```

## Networking: pasta

### Why pasta (not routed TAP or tc redirect)

| Approach | Problem |
|----------|---------|
| **tc redirect** (fctr today) | L2 passthrough bypasses iptables entirely. ztunnel can't see traffic. |
| **Routed TAP + `istio.io/reroute-virtual-interfaces`** | Works for outbound, but the annotation's PREROUTING REDIRECT rule captures ALL traffic on the TAP — including return traffic from the executor to the agent. Executor can't talk to the agent via HTTP over the TAP. Requires vsock relay as workaround. Also requires DNS DNAT workaround. |
| **pasta** | L2→L4 translation in a dedicated netns. Outbound traffic becomes normal sockets in the pod's root netns — ztunnel captures via OUTPUT chain. Port forwarding lets the executor talk to the agent on localhost. No annotations, no iptables workarounds, no vsock relay. |

### How pasta works

pasta sits between two network namespaces:

- **Outbound (agent → external):** Agent sends L2 Ethernet frame on TAP → pasta reads it → pasta opens a TCP/UDP socket in the pod's root netns → payload exits through eth0 as normal socket I/O → ztunnel captures via OUTPUT chain → HBONE → waypoint → destination.
- **Inbound (executor → agent via port forwarding):** pasta binds `localhost:8080` in the pod's root netns → executor connects → pasta wraps data into L2 frames → injects into TAP → agent receives on its eth0.

The pod's addresses, routes, and sysctls are never modified. The executor remains fully functional while the VM runs.

### pasta setup (once at pod startup)

1. Executor starts the pasta process, which creates a dedicated netns with a TAP device.
2. pasta discovers the pod's network interfaces and configures outbound translation (`--outbound-if4`, `--outbound-if6`).
3. pasta forwards the configured port range (e.g., 8080) from the root netns into the dedicated netns.
4. The Firecracker jailer runs inside the dedicated netns (via `setns`).
5. Per-execution cost is zero — the netns and TAP are reused across VM lifecycles.

### DNS

Agent DNS queries go through pasta → UDP socket in root netns → ztunnel captures via OUTPUT chain. Uses the pod's `/etc/resolv.conf` (cluster DNS). No DNAT workaround needed.

For DNS isolation (preventing agents from discovering internal services), a per-tenant CoreDNS deployment can be used alongside CiliumNetworkPolicy L7 DNS filtering — see the Network Isolation proposal for details.

### Comparison with previous design

| | **Previous (routed TAP)** | **Current (pasta)** |
|---|---|---|
| VM netns | Pod's root netns | Dedicated netns (pasta-created) |
| Executor → agent | vsock relay (couldn't use TAP) | `localhost:8080` via pasta port forwarding |
| Agent → external | TAP + reroute annotation | pasta socket in root netns → ztunnel OUTPUT |
| Pod annotation | `istio.io/reroute-virtual-interfaces` | None |
| DNS workaround | DNAT to `127.0.0.1:15053` | None |
| Init complexity | vsock protocol, HTTP proxy, EmitEvent relay | Mount rootfs, configure network, exec agent |

## Guest Init

The guest init is PID 1 inside the Firecracker VM. With pasta, it simplifies to pure system setup:

1. Mount pseudo-filesystems (`/proc`, `/sys`, `/dev`)
2. Wait for rootfs block device (`/dev/vda`)
3. Mount rootfs as overlayfs (read-only lower + tmpfs upper)
4. Read image config from `/etc/image-config.json` (entrypoint, port, env)
5. Configure network (IP, gateway, MTU — received via vsock Init RPC)
6. Write injected files (`/etc/resolv.conf`, `/etc/hosts`)
7. Exec into the agent binary (replaces init as PID 1)

The agent process takes over as PID 1 and listens on its configured port. The executor talks to it directly via pasta's port forwarding on `localhost:8080`.

### vsock (retained for config delivery only)

vsock is still used for one purpose: delivering guest configuration at boot. The guest init dials the executor over vsock and calls `Init` to receive network config and injected files. This is necessary because the guest has no other way to learn its network configuration before it can set up the network.

The `EmitEvent` / SSE relay RPC is removed — the executor reads the SSE stream directly from the agent via HTTP.

## HTTP Server

Three endpoints on port 9090:

### GET /healthz

Readiness probe. Returns 200 when the executor is initialized and in IDLE state. The pool operator's informer watches pod readiness to promote warming → available.

### POST /run

Accepts work from the waypoint. Headers carry claim and execution IDs; body carries the client payload.

```
Request:
  X-Claim-Id: clm-a1b2c3
  X-Execution-Id: exec-456
  Body: <client payload>

Response:
  200 OK
  Content-Type: text/event-stream
  Body: SSE stream (proxied from agent on localhost:8080)

  503 Service Unavailable (executor busy, state != IDLE)
```

`/run` boots a VM, waits for the agent to become ready, forwards the payload as `POST http://localhost:8080/run`, and streams the SSE response back to the waypoint. When the stream ends, the executor tears down the VM and releases the claim.

### GET /status

Returns executor state and current execution ID. For debugging.

## Graceful Shutdown & Error Handling

### Shutdown triggers

- **Normal completion**: SSE stream ends. Agent exits, VM shuts down, executor tears down.
- **Client disconnect**: Waypoint closes the HTTP connection. Executor kills the Firecracker process.
- **Timeout**: Configurable per-execution timeout (`EXEC_TIMEOUT`). Executor kills the Firecracker process.
- **Lease expiry**: Pool operator deletes the pod. Kubelet sends SIGTERM, executor kills any running VM.

### Timeouts

| Phase | Timeout | On timeout |
|-------|---------|------------|
| VM boot (STARTING) | `BOOT_TIMEOUT` (default 30s) | Kill Firecracker process, return 500 |
| Agent startup | `READY_TIMEOUT` (default 10s) | Kill Firecracker process, return 500 |
| Execution (RUNNING) | `EXEC_TIMEOUT` (default 5m) | Kill Firecracker process, return 504 |

### Error responses from /run

| Scenario | HTTP status | Body |
|----------|-------------|------|
| Executor busy (state != IDLE) | 503 | `{"error": "executor busy"}` |
| VM boot failure | 500 | `{"error": "vm boot failed: <detail>"}` |
| Agent startup timeout | 500 | `{"error": "agent not ready within timeout"}` |
| Execution timeout | 504 | `{"error": "execution timeout"}` |
| VM crash mid-execution | 502 | `{"error": "vm exited unexpectedly"}` |

In all error cases, the executor transitions through TEARDOWN → IDLE and releases the lease.

## State Machine

Enforces serial execution with session affinity. One VM at a time per pod, but the VM can persist between requests (paused) if the same session resumes:

```
IDLE → STARTING → RUNNING → WARM (paused, has session) → RUNNING (unpaused)
                      ↓                   ↓ (idle timeout)
                   TEARDOWN ← ← ← ← ← TEARDOWN
                      ↓
                    IDLE
```

- **IDLE**: No VM running. Ready to accept `/run`. `/healthz` returns 200.
- **STARTING**: VM booting (cold start) or unpausing (warm resume). `/run` returns 503.
- **RUNNING**: Agent handling request, SSE streaming. `/run` returns 503.
- **WARM**: VM paused (Firecracker pause), session state in memory. Accepts `/run` for the same session (unpause) or a different session (teardown + cold start). `/healthz` returns 200.
- **TEARDOWN**: VM shutting down. `/run` returns 503.

Transitions:
- `IDLE → STARTING`: New request arrives, boot a fresh VM.
- `STARTING → RUNNING`: Agent is ready, forwarding payload.
- `RUNNING → WARM`: Request complete, pause the VM. Keep it warm for potential resume.
- `RUNNING → TEARDOWN`: Error or client disconnect, kill the VM.
- `WARM → STARTING`: Same session resumes (unpause) or different session (teardown + reboot).
- `WARM → TEARDOWN`: Idle timeout exceeded, tear down the warm VM.
- `TEARDOWN → IDLE`: VM killed, resources cleaned up.

### Session matching on WARM

When `/run` arrives while the executor is in WARM state:
- **Same session ID**: Unpause the VM, forward the new request. Fast resume (~ms).
- **Different session ID**: Tear down the warm VM, boot a fresh one. The pool operator should prefer a fresh IDLE pod over evicting a warm session, but if no IDLE pods are available, eviction is acceptable.

## Pool Operator Integration

### Session-aware claiming

The pool operator's `Claim` RPC accepts an optional `session_id`:

```
ClaimRequest {
  pool: "poc-agent"
  session_id: "sess-abc123"   // empty for new sessions
}

ClaimResponse {
  pod_ip: "10.244.1.15"
  pod_port: 9090
  claim_id: "clm-xyz"
  warm: true                  // pod already has this session loaded
}
```

Selection priority:
1. **Warm pod with matching session** → best case (unpause, no boot)
2. **IDLE pod (no VM running)** → cold start (boot VM)
3. **Warm pod with different session** → evict (tear down old VM, boot new)

The `warm` field tells the waypoint whether the pod has the session loaded. The waypoint forwards this as `X-Warm: true` to the executor so it knows to unpause rather than boot.

### Session ID flow

- **New session**: Client sends request without `X-Session-Id`. The ingress generates a random session ID, injects it as `X-Session-Id` header. The response includes `X-Session-Id` so the client can use it for resume.
- **Resume**: Client sends request with `X-Session-Id: sess-abc123`. The ingress passes it through. The waypoint includes it in the Claim request for session-aware routing.

### Executor contract

1. **Readiness probe**: `/healthz` returns 200 when IDLE or WARM.
2. **Lease renewal**: On `/run`, start a goroutine calling gRPC `Renew` every `leaseTTL/3`.
3. **Release on completion**: After the request completes and the VM is paused (WARM), call gRPC `Release`. The pool operator marks the pod as warm-available, not idle-available.
4. **Report session**: On Release, include the session ID so the pool operator can track which pod has which session warm.
5. **Idle timeout**: After configurable idle time in WARM state (e.g., 5 minutes), tear down the VM and transition to IDLE. Call Release again with no session (pod is now fully idle).

### Configuration

**Environment variables** (set in ExecutorPool pod template):

| Env Var | Default | Purpose |
|---------|---------|---------|
| `LISTEN_ADDR` | `:9090` | HTTP server listen address |
| `POOL_OPERATOR_ADDR` | — | Pool operator gRPC address |
| `LEASE_TTL` | `30s` | Lease duration (renewal interval = TTL/3) |
| `IMAGE_DIR` | `/opt/firecracker` | Kernel, initramfs, rootfs, image-config.json |
| `WORKLOAD_DIR` | `/workload` | Per-execution working directory |
| `VCPUS` | `1` | Number of vCPUs per VM |
| `MEMORY_MB` | `256` | Memory per VM in megabytes |
| `BOOT_TIMEOUT` | `30s` | Max time for VM boot |
| `WARM_IDLE_TIMEOUT` | `5m` | How long to keep a paused VM before teardown |

**Image config** (`image-config.json` in rootfs, read by both executor and guest init):

```json
{"entrypoint": ["/usr/bin/agent"], "port": 8080, "env": {"PYTHONUNBUFFERED": "1"}}
```

## End-to-End Request Flow

### New session (cold start)

```
 1. Client sends POST /a2a/tenant-a/my-agent to NLB (no X-Session-Id)
 2. AgentGateway ingress validates JWT, generates X-Session-Id: sess-abc123
 3. ztunnel → HBONE → waypoint
 4. Waypoint calls gRPC Claim(pool, session_id="sess-abc123")
    Pool operator: no warm pod for this session → returns fresh IDLE pod
 5. Waypoint forwards to executor:
      POST http://{pod_ip}:9090/run
      X-Claim-Id: clm-xyz, X-Session-Id: sess-abc123
      Body: <client payload>
 6. Executor (IDLE → STARTING):
    a. Boots Firecracker VM in pasta's dedicated netns
    b. Guest init configures network, execs into agent
    c. Polls localhost:8080/healthz until agent ready
 7. Executor (STARTING → RUNNING):
    a. POST http://localhost:8080/run with payload + X-Session-Id
    b. Streams SSE response back to waypoint
 8. Agent outbound calls (LLM, tools, A2A):
    Agent → TAP → pasta connect() → eBPF rewrites to proxy
    → proxy logs request to MongoDB → forwards to real dest
    → ztunnel → waypoint (credential injection) → external API
    → response → proxy logs to MongoDB → back to agent
 9. SSE stream ends
10. Executor (RUNNING → WARM):
    a. Pauses VM (Firecracker pause — CPU usage drops to zero)
    b. Stores session_id = "sess-abc123"
    c. Stops lease renewal, calls gRPC Release(claim_id, session_id)
    d. Pool operator marks pod as warm with session sess-abc123
11. Response includes X-Session-Id: sess-abc123 (client stores it)
```

### Resume session (warm hit)

```
 1. Client sends POST /a2a/tenant-a/my-agent with X-Session-Id: sess-abc123
 2. Ingress passes X-Session-Id through
 3. Waypoint calls gRPC Claim(pool, session_id="sess-abc123")
    Pool operator: pod-xyz is warm with this session → returns it (warm: true)
 4. Waypoint forwards to executor:
      POST http://{pod_ip}:9090/run
      X-Claim-Id: clm-new, X-Session-Id: sess-abc123, X-Warm: true
      Body: <resume payload>
 5. Executor (WARM → STARTING):
    a. Unpauses VM (near-instant, ~ms)
 6. Executor (STARTING → RUNNING):
    a. POST http://localhost:8080/run with payload
    b. Agent resumes with in-memory state from previous request
    c. Streams SSE response
 7. SSE stream ends → RUNNING → WARM (pause again)
```

### Resume session (warm miss — session was evicted)

```
 1. Same as warm hit, but pool operator has no warm pod for this session
 2. Pool operator returns a fresh IDLE pod (warm: false)
 3. Executor boots a fresh VM (cold start)
 4. Agent loads session state from tenant MongoDB using the session ID
 5. Execution proceeds normally
```

### Idle timeout

```
Pod in WARM state for > idle timeout (e.g., 5 minutes):
  Executor (WARM → TEARDOWN):
    a. Kills Firecracker process
    b. Cleans up
    c. Calls gRPC Release(claim_id, session_id="") — no session
    d. TEARDOWN → IDLE (fully available again)
```

## Package Layout

```
executor/
├── cmd/
│   ├── executor/main.go          # Entry point: load config, start HTTP server
│   └── init/main.go              # Guest PID 1: mount rootfs, configure net, exec agent
├── internal/
│   ├── server/                   # HTTP handlers (/healthz, /run, /status)
│   ├── executor/                 # State machine, VM lifecycle, run orchestration
│   ├── lease/                    # Pool operator gRPC client (renew/release)
│   ├── pasta/                   # pasta process lifecycle, port forwarding, cgroup setup
│   ├── proxy/                   # HTTP forward proxy + eBPF connect4 interception
│   ├── vm/                       # Firecracker SDK wrapper (boot in pasta netns, stop, pause/resume)
│   ├── vsock/                    # Host-side ttrpc server (Init RPC only, for config delivery)
│   ├── image/                    # Image config loading (entrypoint, port, env)
│   └── config/                   # Executor configuration (env vars)
├── bpf/
│   └── connect4.c                # eBPF cgroup/connect4 program (compiled to .o)
├── proto/
│   └── init.proto                # vsock Init RPC definition (config delivery only)
├── pkg/
│   └── poolpb/                   # Generated gRPC stubs for pool operator
├── Dockerfile
├── go.mod
└── Makefile
```

## What We Reference

- **Srinidhi's Network Isolation proposal**: pasta integration patterns, Firecracker in dedicated netns via jailer `setns`, port forwarding configuration
- **fctr** (`~/10gen/agentic-platform/fctr/`): Guest init patterns (overlayfs, network config with RTNH_F_ONLINK), Firecracker SDK usage, jailer setup

## Event Logging & Replay Cache (eBPF Proxy)

The executor transparently intercepts all outbound HTTP/HTTPS traffic from the agent using a **cgroup/connect4 eBPF program** attached to pasta's cgroup. This captures every LLM call, tool call, A2A call, and MCP call — without the agent knowing or needing to support `HTTP_PROXY`.

### Architecture

```
Agent → TAP → pasta calls connect(api.anthropic.com:443)
  → cgroup/connect4 eBPF fires:
    1. Saves original dest to BPF map (keyed by socket cookie)
    2. Rewrites dest to 127.0.0.1:3128
  → pasta connects to 127.0.0.1:3128 (proxy in executor process)
  → proxy:
    a. Looks up original dest from BPF map
    b. Assigns step number (sequential per session)
    c. Logs {session_id, step, request} to tenant MongoDB
    d. Opens new connection to real dest (goes through ztunnel → waypoint)
    e. Receives response
    f. Logs {session_id, step, response} to tenant MongoDB
    g. Returns response to pasta → agent
```

### Why cgroup/connect4

The eBPF program attaches to pasta's cgroup and intercepts at the `connect()` syscall — before the packet enters iptables. This means:
- **No conflict with ztunnel**: ztunnel's iptables REDIRECT rules are unaffected. The proxy's own outbound connections (from the executor's cgroup, not pasta's) go through ztunnel normally.
- **Fully transparent**: No HTTP_PROXY env var, no iptables insertion ordering. Works with any HTTP client in any language.
- **Scoped**: Only affects pasta's sockets (cgroup-scoped). The executor's own HTTP server, lease client, and health checks are unaffected.
- **127.0.0.1 exemption**: The rewritten destination (127.0.0.1:3128) is exempted from ztunnel's REDIRECT rule, so the connection goes directly to the proxy.

### Setup

1. **Executor creates a cgroup** for pasta (e.g., `/sys/fs/cgroup/pasta-proxy/`).
2. **Executor starts pasta** inside this cgroup.
3. **Executor loads the eBPF program** using `cilium/ebpf` Go library:
   - `BPF_PROG_TYPE_CGROUP_SOCK_ADDR` program, attach type `BPF_CGROUP_INET4_CONNECT`
   - Attached to the pasta cgroup
   - Intercepts TCP connect() to ports 80 and 443
   - Saves original destination to a `BPF_MAP_TYPE_HASH` keyed by `bpf_get_socket_cookie()`
   - Rewrites destination to 127.0.0.1:3128
4. **Executor runs the HTTP proxy** on 127.0.0.1:3128 (part of the executor process, not a separate binary).

### Proxy behavior

For each intercepted connection:
1. Accept connection on :3128
2. Read socket cookie → look up original destination from BPF map
3. Handle HTTPS CONNECT tunneling (for TLS traffic)
4. Log `{session_id, step, request_url, request_headers, request_body}` to tenant MongoDB
5. Open connection to original destination (this outbound goes through ztunnel → waypoint → credential injection)
6. Forward request, receive response
7. Log `{session_id, step, response_status, response_body}` to tenant MongoDB
8. Return response to agent

### Replay cache (on resume after warm miss)

When an agent resumes from a checkpoint (cold start, loading state from MongoDB), it re-executes from the last checkpoint. The proxy provides idempotent replay:

1. Agent re-executes step 3 (e.g., call LLM with the same prompt)
2. Proxy checks: session_id + step 3 already in MongoDB?
3. Yes → return cached response without making the actual call
4. No → forward normally, log result

Step numbers are assigned sequentially per session. The proxy increments a counter for each outbound call. On resume, the counter starts from the checkpoint's last step.

### What this captures

| Traffic | Captured? | Notes |
|---------|-----------|-------|
| LLM API calls (Anthropic, OpenAI) | Yes | HTTPS, ports 443 |
| A2A calls | Yes | HTTP/HTTPS |
| MCP calls | Yes | HTTP/HTTPS |
| REST tool calls | Yes | HTTP/HTTPS |
| MongoDB Atlas | No | TCP wire protocol, port 27017. Not HTTP — eBPF could capture the connect() but the proxy can't parse the wire protocol. Atlas connections are logged at the waypoint level via OTel. |

### Requirements

- **Kernel**: 5.7+ for `BPF_CGROUP_INET4_CONNECT` (EKS Amazon Linux 2023 has kernel 6.1)
- **Capabilities**: `CAP_BPF` + `CAP_NET_ADMIN` (already have `NET_ADMIN`)
- **Go library**: `cilium/ebpf` for loading and managing eBPF programs and maps
- **Pod security**: The executor container needs to mount the BPF filesystem (`/sys/fs/bpf`)

## Pre-execution Snapshots (Future)

Take a Firecracker snapshot after the agent starts but before any session-specific work. The snapshot captures the "agent is initialized and listening" state, which is common across all sessions of the same agent image:

```
First cold boot of agent image:
  Boot VM → init → agent starts → agent ready → PAUSE → snapshot → RESUME → handle request

Subsequent cold boots (any session):
  Restore from snapshot → agent immediately ready → handle request
```

The snapshot is keyed by agent image hash (content-addressed, like fctr's snapshot cache). Boot time drops from seconds to ~150ms. This is the same pattern as Lambda SnapStart.

Not implemented in the initial version — requires the fctr snapshot system (snapshot capture, content-addressed cache, GC). Can be added later as an optimization without changing the executor's external interface.

## Open Questions

- **DNS isolation:** pasta routes DNS through the pod's normal DNS path (ztunnel or cluster DNS). For tenant isolation, a per-tenant CoreDNS deployment with CiliumNetworkPolicy L7 DNS filtering is the recommended approach (see Network Isolation proposal). This is a platform-wide concern, not executor-specific.
- **pasta process failure:** If pasta dies, the VM loses network connectivity. The executor should monitor the pasta process and reject new `/run` calls if pasta is not running. The pod should be replaced (fail-closed).
- **Firecracker in pasta netns:** The Firecracker jailer needs to `setns` into pasta's dedicated netns. Srinidhi's proposal has this working — we reference their implementation.
- **Warm VM memory accounting:** A paused VM still holds its memory allocation. With many warm VMs across pods on the same node, memory pressure could become an issue. Need to size node resources accordingly and potentially set a max-warm-per-node limit.
- **Session state on warm miss:** When a warm pod isn't available for a session resume, the agent needs to load state from tenant MongoDB. The agent must handle both cases (warm: in-memory state available, cold: load from DB). This is the agent's responsibility, not the executor's.

## What We Explicitly Do Not Include

- **tc redirect networking** — replaced by pasta
- **`istio.io/reroute-virtual-interfaces` annotation** — not needed with pasta
- **DNS DNAT workaround** — not needed with pasta
- **vsock EmitEvent / SSE relay** — executor talks to agent directly via localhost
- **Per-request VM boot/teardown** — VMs persist across requests (paused when idle)
- **ttrpc external API** — replaced by HTTP
- **One-shot mode** — executor is always long-lived
- **SecureToolWrapper** — agent makes plain HTTP calls; mesh handles policy transparently
