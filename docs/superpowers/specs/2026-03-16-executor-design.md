# Executor Design Spec

**Date:** 2026-03-16
**Status:** Draft
**Author:** Siyanzhu + Claude

## Problem

The current Magenta executor (`fctr`) uses `tc redirect` for VM networking, which bypasses L3/iptables entirely and is incompatible with both Istio ambient mesh (ztunnel) and the Tenant Egress Gateway. Its external API is ttrpc over a Unix socket, designed for one-shot execution, with no concept of warm pools, claim leases, or request proxying. It cannot integrate with the pool operator or the ambient mesh architecture.

We need an executor that:

1. Uses routed TAP networking compatible with Istio ambient mesh (`istio.io/reroute-virtual-interfaces`)
2. Integrates with the pool operator (claim/renew/release lifecycle)
3. Accepts HTTP requests from the waypoint and proxies payloads into the VM via vsock
4. Runs as a long-lived process handling sequential requests (pool model, not one-shot)
5. Streams SSE responses back through the waypoint to the client

## Solution

A new executor binary written from scratch, referencing `fctr` for Firecracker SDK usage, TAP creation, and guest init patterns. The executor runs as a long-lived HTTP server inside a pod managed by the pool operator. Each request boots a fresh Firecracker VM, forwards the client payload via virtio-vsock, streams the SSE response back, tears down the VM, and releases the claim.

## Architecture

```
┌──────────────────────────────────────────────────────────────┐
│  Executor Pod (c5.metal node, ambient mesh enrolled)         │
│                                                              │
│  ┌────────────────────────────────────────────────────────┐  │
│  │  Executor Process (host, PID 1 of container)           │  │
│  │                                                        │  │
│  │  HTTP Server (:9090)                                   │  │
│  │    GET  /healthz  → readiness probe                    │  │
│  │    POST /run      → boot VM, proxy request, stream SSE │  │
│  │                                                        │  │
│  │  Lease Client                                          │  │
│  │    POST /renew  (every leaseTTL/3 during execution)    │  │
│  │    POST /release (on completion)                       │  │
│  │                                                        │  │
│  │  State Machine                                         │  │
│  │    IDLE → STARTING → RUNNING → TEARDOWN → IDLE         │  │
│  └──────────┬──────────────────────────┬──────────────────┘  │
│             │ vsock (control)          │ routed TAP (data)   │
│             │                          │                     │
│  ┌──────────▼──────────────────────────▼──────────────────┐  │
│  │  Firecracker VM                                        │  │
│  │                                                        │  │
│  │  Guest Init (PID 1)                                    │  │
│  │    Dials vsock → calls Init → gets config + payload    │  │
│  │    Configures eth0 (169.254.1.2)                       │  │
│  │    Starts agent on localhost:8080                       │  │
│  │    Forwards payload → agent                            │  │
│  │    Calls Stream → pushes SSE chunks back over vsock    │  │
│  │                                                        │  │
│  │  Agent Process (localhost:8080)                         │  │
│  │    Handles request, returns SSE stream                  │  │
│  │    Outbound calls → eth0 → TAP → ztunnel → waypoint   │  │
│  └────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────┘
```

## Networking: Routed TAP

Replaces fctr's `tc redirect` with a routed TAP that is compatible with Istio ambient mesh.

### Why tc redirect doesn't work

fctr's `tc redirect` installs TC-layer filters that shuttle packets directly between the TAP device and eth0 at L2. Packets never enter the L3 stack, so iptables rules (including ztunnel's REDIRECT rules) never fire. This is incompatible with both ztunnel and the TEG — neither can see the traffic.

### Routed TAP approach

**Setup (per-execution):**

1. Create TAP device `fctr-tap0` (IFF_VNET_HDR | IFF_NO_PI, owner=JailerUID, MTU matching host)
2. Assign link-local IP to TAP host side: `169.254.1.1/32`
3. Add route to guest via TAP: `ip route add 169.254.1.2/32 dev fctr-tap0`
4. Add DNS interception rule: `iptables -t nat -A ISTIO_PRERT -i fctr-tap0 -p udp --dport 53 -j DNAT --to-destination 127.0.0.1:15053`

Note: Step 4 is needed because the `istio.io/reroute-virtual-interfaces` annotation only adds a TCP REDIRECT rule. DNS (UDP port 53) requires explicit DNAT to ztunnel's DNS proxy, which listens on `127.0.0.1:15053` (localhost-bound). REDIRECT would rewrite the destination to the TAP interface IP, which ztunnel's DNS socket doesn't accept. DNAT to `127.0.0.1:15053` sends it to the right address.

No tc redirect filters. No address/route release from eth0.

**Guest config (via vsock Init):**

- IP: `169.254.1.2/32`
- Gateway: `169.254.1.1`
- MTU: discovered from host interface

**Guest routing note:** The guest's default route (`ip route add default via 169.254.1.1`) requires `RTNH_F_ONLINK` because the gateway (`169.254.1.1`) is not within the guest's `/32` prefix. Without this flag, the kernel rejects the route with `ENETUNREACH`. The fctr guest init already handles this pattern (`fctr/internal/init/net.go:163`).

**Teardown (per-execution):**

1. Remove DNS interception rule: `iptables -t nat -D ISTIO_PRERT -i fctr-tap0 -p udp --dport 53 -j DNAT --to-destination 127.0.0.1:15053`
2. Delete route: `ip route del 169.254.1.2/32 dev fctr-tap0`
3. Delete TAP device

**Pod annotation:**

```yaml
annotations:
  istio.io/reroute-virtual-interfaces: "fctr-tap0"
```

This tells istio-cni to add an iptables rule in the pod's PREROUTING chain:

```
-A ISTIO_PRERT -i fctr-tap0 -p tcp -j REDIRECT --to-ports 15001
```

Traffic from the VM arriving on the TAP enters L3 via PREROUTING, hits this rule, and gets redirected to ztunnel's outbound port. ztunnel wraps it in HBONE mTLS and routes it through the tenant waypoint, which applies credential injection, prompt guards, and observability.

### Why vsock for host-to-guest (not HTTP over TAP)

The executor process runs in the pod's network namespace. If it sends HTTP to the VM at `169.254.1.2`, ztunnel's OUTPUT chain rule intercepts it:

```
-A ISTIO_OUTPUT ! -d 127.0.0.1/32 -p tcp -m mark ! --mark 0x539/0xfff -j REDIRECT --to-ports 15001
```

This would route executor-to-guest control traffic through the mesh, which is not what we want. Virtio-vsock bypasses the network stack entirely — it's a hypervisor-level memory-based IPC channel between the VMM process and the guest kernel. No IP packets, no iptables, no ztunnel interception.

This cleanly separates:
- **Control plane** (executor ↔ guest): vsock — invisible to the mesh
- **Data plane** (agent ↔ external services): TAP → ztunnel → waypoint — goes through the mesh

## vsock Protocol

Two RPCs between executor (host, server) and guest init (client), over virtio-vsock using ttrpc:

### Init

Guest init calls at boot. Executor responds with config and the client payload in one message.

**Response contains:**
- Network config: IP, gateway, MTU (discovered from pod's network)
- Process config: command, args, env (from executor's environment)
- Files: `/etc/resolv.conf` (read from pod's filesystem), `/etc/hosts`, `/etc/hostname`
- Hostname: execution ID
- Payload: the client's request body (forwarded from the waypoint's `/run` request)

The rootfs image is generic — environment-specific details come from the executor at runtime via Init, not baked into the image.

### Stream

Guest init calls after configuring the network, starting the agent, and forwarding the payload to the agent. Pushes SSE chunks from the agent's response back to the executor over vsock. The executor proxies these chunks to the waypoint's HTTP response.

Stream completing signals the request is done. The executor detects VM exit by watching the Firecracker process.

## Graceful Shutdown & Error Handling

### Shutdown trigger

The executor needs to stop the VM in several scenarios:
- **Normal completion**: SSE stream ends (agent returned final response). Guest init exits after Stream completes, VM shuts down naturally.
- **Client disconnect**: Waypoint closes the HTTP connection (client gone). Executor detects the closed connection, kills the Firecracker process directly (`SIGKILL` to VMM). No graceful agent shutdown — the client is already gone.
- **Timeout**: Configurable per-execution timeout (e.g., `EXEC_TIMEOUT=5m`). If exceeded, executor kills the Firecracker process.
- **Lease expiry**: If the pool operator deletes the pod (lease not renewed), the kubelet sends SIGTERM to the executor. Executor kills any running VM and exits.

### Timeouts

| Phase | Timeout | On timeout |
|-------|---------|------------|
| VM boot (STARTING) | `BOOT_TIMEOUT` (default 30s) | Kill Firecracker process, return 500 to waypoint |
| Guest Init dial + config | Included in boot timeout | Same as above |
| Agent startup (Init → Stream) | `READY_TIMEOUT` (default 10s) | Kill Firecracker process, return 500 |
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
  Body: SSE stream (proxied from agent via vsock)

  503 Service Unavailable (executor busy, state != IDLE)
```

`/run` blocks for the duration of the execution — the response is the SSE stream from the agent. When the stream ends, the executor tears down the VM and releases the claim. The waypoint (and client) see a single long-lived HTTP connection with streaming response.

### GET /status

Returns executor state (IDLE/STARTING/RUNNING/TEARDOWN) and current execution ID. For debugging, not required by the pool operator.

## State Machine

Enforces serial execution (one VM at a time per pod):

```
IDLE → STARTING → RUNNING → TEARDOWN → IDLE
```

- **IDLE**: Ready to accept `/run`. `/healthz` returns 200.
- **STARTING**: VM booting, network being configured. `/run` returns 503. `/healthz` returns 503.
- **RUNNING**: Agent handling request, SSE streaming. `/run` returns 503. `/healthz` returns 503.
- **TEARDOWN**: VM shutting down, TAP being removed, lease being released. `/run` returns 503. `/healthz` returns 503.

The state machine is the mechanism that implements the pool operator's "mutex guard on `/run`" requirement. If a new request arrives before teardown completes, it gets 503 and the waypoint retries with a different pod.

## Pool Operator Integration

### Executor contract

The executor's responsibilities are minimal:

1. **Readiness probe**: `/healthz` returns 200 only when IDLE. Pool operator promotes warming → available when pod becomes Ready.
2. **Lease renewal**: On `/run`, start a goroutine calling `POST /renew` every `leaseTTL/3`. If renewal stops, the operator deletes the pod after `leaseTTL` expires.
3. **Release on completion**: After teardown, call `POST /release`. Best effort, 3 retries (1s/2s/4s backoff). Lease expiry is the safety net.
4. **Reuse safety**: Tear down VM, TAP, and working directory before calling `/release`. State machine prevents concurrent `/run`.

The executor does NOT register, heartbeat outside claims, deregister, or manage pool state.

### Configuration

| Env Var | Default | Purpose |
|---------|---------|---------|
| `LISTEN_ADDR` | `:9090` | HTTP server listen address |
| `POOL_OPERATOR_ADDR` | — | Pool operator service address |
| `LEASE_TTL` | `30s` | Lease duration (renewal interval = TTL/3) |
| `AGENT_COMMAND` | — | Command to run inside VM (e.g., `python /agent/serve.py`) |
| `AGENT_PORT` | `8080` | Port the agent listens on inside the VM |
| `IMAGE_DIR` | `/opt/firecracker` | Directory containing kernel, initramfs, rootfs |
| `WORKLOAD_DIR` | `/workload` | Working directory for chroot, logs |
| `VCPUS` | `1` | Number of vCPUs per VM |
| `MEMORY` | `256M` | Memory per VM |
| `BOOT_TIMEOUT` | `30s` | Max time for VM boot |
| `READY_TIMEOUT` | `10s` | Max time for agent to start after VM boots |
| `EXEC_TIMEOUT` | `5m` | Max execution time per request |

## End-to-End Request Flow

```
 1. Client sends POST /a2a/tenant-a/my-agent to NLB
 2. AgentGateway ingress validates JWT (ExtAuth), matches HTTPRoute
 3. ztunnel on ingress node → HBONE → ztunnel on executor node → waypoint
 4. Waypoint calls POST /claim to pool operator → gets {pod_ip, claim_id}
 5. Waypoint forwards to executor:
      POST http://{pod_ip}:9090/run
      X-Claim-Id: clm-a1b2c3
      X-Execution-Id: exec-456
      Body: <client payload>
 6. Executor:
    a. Reads claim ID and execution ID from headers
    b. Transitions IDLE → STARTING
    c. Starts lease renewal goroutine
    d. Creates routed TAP (169.254.1.1 ↔ 169.254.1.2)
    e. Boots Firecracker VM (kernel + rootfs + vsock)
    f. Guest init dials vsock, calls Init
    g. Executor responds with {config, payload}
 7. Guest init:
    a. Configures eth0 (169.254.1.2, gateway 169.254.1.1)
    b. Writes /etc/resolv.conf, /etc/hosts, /etc/hostname
    c. Starts agent process on localhost:8080
    d. Sends HTTP request to localhost:8080 with payload
    e. Calls Stream RPC, pushes SSE chunks back over vsock
 8. Executor:
    a. Transitions STARTING → RUNNING
    b. Proxies SSE chunks from vsock to the HTTP response
 9. During execution, agent makes outbound calls (LLM, tools, A2A):
    Agent → eth0 (169.254.1.2) → TAP → iptables PREROUTING
    → reroute-virtual-interfaces rule → ztunnel port 15001
    → HBONE → tenant waypoint (credential injection, prompt guard)
    → external API
10. SSE stream ends
11. Executor:
    a. Transitions RUNNING → TEARDOWN
    b. VM shuts down (guest init exits, Firecracker process terminates)
    c. Tears down TAP, cleans working directory
    d. Stops lease renewal goroutine
    e. Calls POST /release to pool operator
    f. Transitions TEARDOWN → IDLE
    g. Ready for next request
```

## Kubernetes Deployment

### ExecutorPool CRD

```yaml
apiVersion: agentic.example.com/v1alpha1
kind: ExecutorPool
metadata:
  name: poc-agent
  namespace: example-executor
spec:
  desired: 3
  leaseTTL: 30s
  warmingTimeout: 5m
  maxSurge: 10
  podTemplate:
    metadata:
      labels:
        app: executor
      annotations:
        istio.io/reroute-virtual-interfaces: "fctr-tap0"
    spec:
      nodeSelector:
        node.kubernetes.io/instance-type: c5.metal
      tolerations:
        - key: workload
          value: agents
          effect: NoSchedule
      initContainers:
        - name: rootfs
          image: <ecr>/poc-agent-rootfs:latest
          command: ["cp", "/rootfs.ext4", "/out/rootfs.ext4"]
          volumeMounts:
            - name: guest-rootfs
              mountPath: /out
      containers:
        - name: executor
          image: <ecr>/executor:latest
          ports:
            - containerPort: 9090
              name: http
          readinessProbe:
            httpGet:
              path: /healthz
              port: 9090
            periodSeconds: 5
          env:
            - name: POOL_OPERATOR_ADDR
              value: "pool-operator.example-executor.svc:8080"
            - name: LEASE_TTL
              value: "30s"
            - name: AGENT_COMMAND
              value: "python /agent/serve.py"
          securityContext:
            capabilities:
              add: [NET_ADMIN, NET_RAW, SYS_ADMIN, MKNOD]
          volumeMounts:
            - name: guest-rootfs
              mountPath: /opt/firecracker/rootfs.ext4
              subPath: rootfs.ext4
              readOnly: true
            - name: dev-kvm
              mountPath: /dev/kvm
            - name: dev-vsock
              mountPath: /dev/vhost-vsock
            - name: dev-tun
              mountPath: /dev/net/tun
      volumes:
        - name: guest-rootfs
          emptyDir: {}
        - name: dev-kvm
          hostPath:
            path: /dev/kvm
        - name: dev-vsock
          hostPath:
            path: /dev/vhost-vsock
        - name: dev-tun
          hostPath:
            path: /dev/net/tun
```

### Namespace enrollment

The executor namespace is enrolled in the ambient mesh:

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: example-executor
  labels:
    istio.io/dataplane-mode: ambient
    istio.io/use-waypoint: agentgateway-waypoint
```

## Package Layout

```
executor/
├── cmd/
│   ├── executor/main.go          # Entry point: load config, start HTTP server
│   └── init/main.go              # Guest PID 1 (referenced from fctr patterns)
├── internal/
│   ├── server/                   # HTTP handlers (/healthz, /run, /status)
│   ├── executor/                 # State machine, VM lifecycle, SSE proxying
│   ├── lease/                    # Pool operator client (renew/release)
│   ├── net/                      # Routed TAP setup/teardown
│   ├── vm/                       # Firecracker SDK wrapper (boot, stop, vsock)
│   ├── vsock/                    # Host-side vsock ttrpc server (Init, Stream)
│   └── config/                   # Configuration (env vars, defaults)
├── Dockerfile                    # Two-stage build (Go build → distroless)
├── go.mod
└── Makefile
```

## What We Reference from fctr

Not forking, but using as reference for implementation patterns:

- **Guest init** (`fctr/internal/init/`): overlayfs setup, network configuration, workload exec, vsock dial pattern
- **Firecracker SDK usage** (`fctr/internal/virt/`): machine config assembly, jailer setup, chroot bind mounts, vsock listener creation
- **TAP creation** (`fctr/internal/net/tap.go`): IFF_VNET_HDR | IFF_NO_PI flags, owner/group setup, MTU matching
- **Config patterns** (`fctr/internal/config/`): env var loading, validation

## Open Questions

- **DNS as information disclosure:** ztunnel's DNS proxy forwards all unknown queries to kube-dns, which means a tenant's agent can resolve internal service names and discover cluster topology without making a connection. This is a platform-wide multi-tenancy concern, not executor-specific. Options include a per-tenant DNS forwarder with an allowlist, per-tenant CoreDNS, or NetworkPolicy-based DNS restriction. See also [istio/istio#54020](https://github.com/istio/istio/issues/54020) for upstream work on UDP/DNS support for the reroute-virtual-interfaces annotation.

## What We Explicitly Do Not Include

- **tc redirect networking** — replaced by routed TAP
- **VM snapshots** — all state is external (tenant MongoDB)
- **ttrpc external API** — replaced by HTTP
- **One-shot mode** — executor is always long-lived
- **Replay cache / execution logs in OE** — waypoint handles observability via OTel; checkpoint state managed externally
- **SecureToolWrapper** — agent makes plain HTTP calls; mesh handles policy transparently
