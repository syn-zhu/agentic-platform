# Executor Design Spec

**Date:** 2026-03-16
**Status:** Draft (v3 — warm VM + session affinity)
**Author:** Siyanzhu + Claude

## Problem

The current Magenta executor (`fctr`) uses `tc redirect` for VM networking (incompatible with Istio ambient mesh), a ttrpc Unix socket API (incompatible with the pool operator), and a one-shot execution model (no warm reuse). We need an executor that integrates with the ambient mesh, the pool operator, and supports session affinity with warm VM reuse.

## Solution

An executor binary that uses [pasta](https://passt.top/) for VM networking and keeps VMs warm between requests using Firecracker's pause/unpause. The executor proxies requests to the agent inside the VM and manages the VM lifecycle (boot, pause, unpause, teardown).

Key properties:
1. **pasta** translates L2↔L4 between the VM's dedicated netns and the pod's root netns. Agent outbound traffic becomes normal sockets captured by ztunnel. The executor talks to the agent on `localhost:8080` via pasta port forwarding.
2. **Warm VMs** — after a request completes, the VM is paused (not killed). If the same session resumes, the VM is unpaused instantly (~ms). After an idle timeout, the VM is torn down and the pod returns to the available pool.
3. **Session-aware routing** — the pool operator tracks which pod has a warm session. On Claim, it prefers returning a warm pod matching the session ID.

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
│  │    State Machine:                                         │ │
│  │      IDLE → STARTING → RUNNING → WARM → RUNNING (resume) │ │
│  │                                    ↓ (idle timeout)       │ │
│  │                                 TEARDOWN → IDLE           │ │
│  │                                                           │ │
│  │  On /run (cold start — IDLE):                             │ │
│  │    Boot VM → wait for agent → forward payload → stream    │ │
│  │  On /run (warm resume — WARM):                            │ │
│  │    Unpause VM → forward payload → stream                  │ │
│  │  On stream end:                                           │ │
│  │    Pause VM → transition to WARM → start idle timer       │ │
│  │                                                           │ │
│  │  pasta process (L2 ↔ L4 translation)                      │ │
│  │    localhost:8080 → agent inside VM                        │ │
│  │                                                           │ │
│  │  eth0 (pod IP)                                            │ │
│  │    outbound from pasta → ztunnel OUTPUT chain capture     │ │
│  └───────────────────────────────────────────────────────────┘ │
│                                                               │
│  ─────────────────── netns boundary ─────────────────────     │
│                                                               │
│  Dedicated netns (created by pasta)                           │
│  ┌───────────────────────────────────────────────────────────┐ │
│  │  TAP device ← pasta                                       │ │
│  │  Firecracker VM                                           │ │
│  │    Agent Process (:8080)                                  │ │
│  │      Running (RUNNING) or Paused (WARM)                   │ │
│  │      Outbound → TAP → pasta → root netns → ztunnel       │ │
│  └───────────────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────────┘
```

## State Machine

```
IDLE → STARTING → RUNNING → WARM → RUNNING (resume, same session)
          ↓           ↓        ↓ (idle timeout)
       TEARDOWN ← TEARDOWN ← TEARDOWN → IDLE
```

- **IDLE**: No VM running. Ready for any session. `/healthz` returns 200.
- **STARTING**: VM booting (cold start). `/run` returns 503.
- **RUNNING**: Agent handling request, SSE streaming. `/run` returns 503.
- **WARM**: VM paused, session cached. `/healthz` returns 200. Accepts `/run` for the same session (transitions to RUNNING via unpause). Returns 503 for a different session unless the executor evicts the warm session first.
- **TEARDOWN**: VM shutting down. `/run` returns 503.

### Healthz behavior

`/healthz` returns 200 in two states:
- **IDLE** — available for any session
- **WARM** — available for the cached session (or for a new session after eviction)

This lets the pool operator see both idle and warm pods as "available" in different ways.

## Session-Aware Claim

The pool operator's `Claim` RPC gains a `session_id` parameter:

```protobuf
message ClaimRequest {
  string pool = 1;
  string session_id = 2;  // empty for new sessions
}

message ClaimResponse {
  string pod_name = 1;
  string pod_ip = 2;
  int32 pod_port = 3;
  string claim_id = 4;
  bool warm = 5;  // true if pod has the session cached
}
```

Pool operator selection logic:
1. **Warm match**: Pod in WARM state with matching session → best case (unpause, ~ms)
2. **Fresh idle**: Pod in IDLE state (no VM) → cold start (boot VM)
3. **Warm evict**: Pod in WARM state with different session → evict (teardown + cold start)

The `warm` field tells the waypoint whether the pod already has the session loaded. The waypoint passes this to the executor as `X-Warm: true` so the executor knows to unpause instead of boot.

## Session ID

- **New session**: The ingress gateway generates a random session ID and injects `X-Session-Id` header.
- **Resume**: The client provides `X-Session-Id` from a previous interaction.
- The session ID flows through the entire chain: client → ingress → waypoint → pool operator (Claim) → executor → agent.

## HTTP Server

### GET /healthz

Returns 200 when IDLE or WARM. Pool operator uses this for readiness.

### POST /run

```
Request:
  X-Claim-Id: clm-a1b2c3
  X-Execution-Id: exec-456
  X-Session-Id: sess-789
  X-Warm: true/false
  Body: <client payload>

Response:
  200 OK (Content-Type: text/event-stream, SSE stream)
  503 Service Unavailable (executor busy)
```

Behavior depends on current state:

**IDLE (cold start):**
1. Transition IDLE → STARTING
2. Boot VM in pasta's netns
3. Wait for agent ready
4. Transition STARTING → RUNNING
5. Forward payload to agent, stream SSE back
6. On stream end: pause VM → transition RUNNING → WARM

**WARM with matching session (resume):**
1. Transition WARM → RUNNING
2. Unpause VM
3. Forward payload to agent, stream SSE back
4. On stream end: pause VM → transition RUNNING → WARM

**WARM with different session (evict + cold start):**
1. Transition WARM → TEARDOWN → IDLE → STARTING
2. Kill old VM, boot new VM
3. Same as cold start from step 3

## Warm VM Lifecycle

### Pause on completion

When the SSE stream ends:
1. Executor **pauses** the Firecracker VM (CPU frozen, memory retained)
2. Records the session ID
3. Transitions RUNNING → WARM
4. Starts the idle timer
5. Stops lease renewal, calls gRPC `Release`
6. Pod returns to the pool as "warm with session X"

### Unpause on resume

When a `/run` arrives with a matching session:
1. Starts lease renewal
2. **Unpauses** the VM (~milliseconds)
3. Transitions WARM → RUNNING
4. Forwards payload to agent (agent still running, still has in-memory state)

### Idle timeout

If no resume arrives within `WARM_TIMEOUT` (default 5 minutes):
1. Kill Firecracker process
2. Clean working directory
3. Transition WARM → TEARDOWN → IDLE
4. Pod returns to pool as fully available

### Eviction

If a `/run` arrives for a different session while WARM:
1. Kill the warm VM
2. Transition WARM → TEARDOWN → IDLE → STARTING
3. Boot a fresh VM for the new session

## Networking: pasta

*(Unchanged from v2 — see previous spec for full details)*

pasta creates a dedicated netns with L2↔L4 translation. Agent outbound becomes normal sockets in the pod's root netns. The executor talks to the agent on `localhost:8080`. No annotations or iptables workarounds needed.

pasta is started once at pod startup and reused across VM lifecycles.

## Guest Init

*(Unchanged from v2)*

Mount pseudo-fs → wait for rootfs → mount overlayfs → vsock Init for config → configure network → write files → exec into agent.

## Graceful Shutdown & Error Handling

### Shutdown triggers

- **Normal completion**: SSE stream ends → pause VM → WARM
- **Client disconnect**: Waypoint closes HTTP connection → kill VM → TEARDOWN → IDLE
- **Boot failure**: VM fails to start → TEARDOWN → IDLE
- **Idle timeout**: WARM for too long → kill VM → TEARDOWN → IDLE
- **Pod deletion**: SIGTERM from kubelet → kill VM → exit

### Error responses from /run

| Scenario | HTTP status | Body |
|----------|-------------|------|
| Executor busy (RUNNING or STARTING) | 503 | `{"error": "executor busy"}` |
| VM boot failure | 500 | `{"error": "vm boot failed: <detail>"}` |
| VM crash mid-execution | 502 | `{"error": "vm exited unexpectedly"}` |
| Unpause failed | 500 | `{"error": "unpause failed: <detail>"}` |

## Pool Operator Integration

### Executor contract

1. **Readiness probe**: `/healthz` returns 200 when IDLE or WARM.
2. **Lease renewal**: On `/run`, renew lease every `leaseTTL/3`.
3. **Release on completion**: When stream ends and VM is paused, call `Release`. Pod returns to pool as warm.
4. **Session tracking**: Executor reports its session ID to the pool operator (via pod labels or Release response) so the operator knows which sessions are warm where.

### Configuration

| Env Var | Default | Purpose |
|---------|---------|---------|
| `LISTEN_ADDR` | `:9090` | HTTP server listen address |
| `POOL_OPERATOR_ADDR` | — | Pool operator gRPC address |
| `LEASE_TTL` | `30s` | Lease duration |
| `IMAGE_DIR` | `/opt/firecracker` | Kernel, initramfs, rootfs, image-config.json |
| `WORKLOAD_DIR` | `/workload` | Per-execution working directory |
| `VCPUS` | `1` | Number of vCPUs per VM |
| `MEMORY_MB` | `256` | Memory per VM in megabytes |
| `BOOT_TIMEOUT` | `30s` | Max time for VM boot |
| `WARM_TIMEOUT` | `5m` | How long to keep a paused VM before teardown |

## End-to-End Request Flow

### New session (cold start)

```
 1. Client sends POST /a2a/tenant-a/my-agent (no session ID)
 2. Ingress generates X-Session-Id: sess-789
 3. Waypoint calls Claim(pool="default", session_id="sess-789")
    → pool operator returns fresh pod (warm=false)
 4. Waypoint forwards to executor:
      POST http://{pod_ip}:9090/run
      X-Claim-Id: clm-a1b2c3, X-Session-Id: sess-789, X-Warm: false
      Body: <payload>
 5. Executor: IDLE → STARTING
    Boots VM, waits for agent, STARTING → RUNNING
    Forwards payload to agent on localhost:8080
    Streams SSE response back to waypoint
 6. Stream ends → executor pauses VM → RUNNING → WARM
    Releases claim. Pod returns to pool as warm (session=sess-789)
 7. Response includes X-Session-Id: sess-789 (client stores it)
```

### Resume session (warm hit)

```
 1. Client sends POST /a2a/tenant-a/my-agent
      X-Session-Id: sess-789
 2. Waypoint calls Claim(pool="default", session_id="sess-789")
    → pool operator returns warm pod (warm=true)
 3. Waypoint forwards to executor:
      POST http://{pod_ip}:9090/run
      X-Claim-Id: clm-b2c3d4, X-Session-Id: sess-789, X-Warm: true
      Body: <payload>
 4. Executor: WARM → RUNNING
    Unpauses VM (~ms), forwards payload to agent
    Agent has in-memory state from previous request
    Streams SSE response back
 5. Stream ends → executor pauses VM → RUNNING → WARM
    Releases claim. Pod stays warm.
```

### Resume session (warm miss — pod was reclaimed)

```
 1. Client sends X-Session-Id: sess-789
 2. Claim(session_id="sess-789") → no warm pod found
    → pool operator returns fresh pod (warm=false)
 3. Executor: IDLE → STARTING → boots VM
    Agent cold starts, loads state from tenant MongoDB using session ID
 4. Same flow as new session from step 5
```

## Future: Pre-Execution Snapshots

A Firecracker snapshot taken after the agent starts but before any session-specific work. Keyed by agent image hash — reusable across all sessions of the same agent.

```
First cold boot of an agent image:
  Boot VM → init → agent starts → agent ready
  → Pause VM → take snapshot (memory + disk)
  → Unpause → continue with request

Subsequent cold boots (any session):
  Restore from snapshot → agent immediately ready (~150ms)
  → Continue with request
```

This is an optimization on top of the warm VM design. It reduces cold start time from seconds to ~150ms. The warm VM (unpause) is still faster (~ms) but the snapshot helps when no warm pod is available.

Implementation deferred to a follow-up.

## Open Questions

- **DNS isolation:** See Network Isolation proposal for per-tenant CoreDNS + CiliumNetworkPolicy approach.
- **pasta process failure:** Executor monitors pasta, rejects `/run` if pasta is dead. Pod replaced.
- **Warm pod selection fairness:** When multiple warm pods exist for different sessions, how does the pool operator decide which to evict? LRU? Session priority?
- **Agent contract for resume:** The agent needs to handle receiving a new request while it has in-memory state from a previous one. Is this "just works" (the agent is a stateful HTTP server) or does it need explicit session management?

## What We Explicitly Do Not Include

- **tc redirect networking** — replaced by pasta
- **Direct-to-agent routing** (executor out of path) — executor must stay in path for pause/unpause
- **Per-request VM teardown** — VMs are paused, not killed, to enable warm reuse
- **Cluster-wide pool operator** — one operator per tenant namespace
- **SecureToolWrapper** — agent makes plain HTTP calls; mesh handles policy transparently
