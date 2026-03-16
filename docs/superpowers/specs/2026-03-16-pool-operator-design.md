# Pool Operator Design Spec

**Date:** 2026-03-16
**Status:** Approved
**Author:** Siyanzhu + Claude

## Problem

The current assignment system (`agentic-platform/assignment`) manages a warm pool of Firecracker executor pods using a standalone Redis-backed service. Executors self-register, heartbeat, and the assignment service uses a Redis Lua script (ZREMRANGEBYSCORE + ZPOPMIN) for atomic claims.

This works for claim/release, but has gaps:

- **No pool management.** Deployment replicas are set manually. Nobody auto-maintains a target idle count or backfills after claims.
- **No queuing signal.** Pool empty = 503. No visibility into whether pods are warming up.
- **Redis SPOF.** Single Redis instance with no HA.
- **No claimed-state visibility.** Assignment service only knows about idle pods. Once claimed, a pod is invisible until it re-registers.
- **No observability.** No metrics on pool utilization, claim latency, or failure rate.

## Solution

A single Go binary ("pool operator") that replaces both the assignment service and Redis. It:

1. **Manages warm pools** — creates/deletes Firecracker executor pods to maintain a target idle count per pool.
2. **Serves claims** — sub-millisecond pod claims from an in-memory pool, called by waypoint.
3. **Accepts releases** — executor calls back when execution completes, pod returns to available pool.
4. **Self-heals** — detects dead/stuck pods via informer + lease expiry, backfills automatically.

### Architecture

```
Waypoint ──POST /claim──▶ ┌─────────────────────────────┐
                          │      Pool Operator           │
Executor ──POST /renew───▶│                              │
         ──POST /release─▶│  In-memory state:            │
                          │    available / claimed /      │
                          │    warming                    │
                          │                              │
                          │  Informer: watches pods      │
                          │  Reconciler: maintains pool  │
                          │  Leader election: K8s Lease  │
                          └──────────────┬───────────────┘
                                         │ creates/deletes
                                         ▼
                                   Executor Pods
```

## API

Four HTTP/JSON endpoints:

### POST /claim

Called by waypoint (via ExtProc) to claim an available executor pod.

```json
// Request
{
    "pool": "poc-agent"
}

// Response 200
{
    "pod_name": "executor-pool-7x2k4",
    "pod_ip": "10.244.1.15",
    "pod_port": 9090,
    "claim_id": "clm-a1b2c3"
}

// Response 503
{
    "error": "no available pods",
    "available": 0,
    "warming": 2
}
```

The operator generates `claim_id`. The executor uses it for `/renew` and `/release`.

### POST /renew

Called by executor periodically (every TTL/3) to extend the lease on a claim. The lease is not a fixed execution deadline — executions can run indefinitely as long as the executor keeps renewing.

```json
// Request
{
    "claim_id": "clm-a1b2c3"
}

// Response 200
{
    "expires_at": "2026-03-16T10:31:00Z"
}

// Response 404
{
    "error": "claim not found"
}
```

### POST /release

Called by executor when execution completes.

```json
// Request
{
    "claim_id": "clm-a1b2c3"
}

// Response 200
{
    "status": "released"
}

// Response 404
{
    "error": "claim not found"
}
```

Executor should treat 404 on release as success (already released or expired).

### GET /status

Observability endpoint. Returns pool metrics per pool name.

```json
// Response 200
{
    "pools": {
        "poc-agent": {
            "desired": 5,
            "available": 3,
            "claimed": 1,
            "warming": 1
        }
    }
}
```

### Prometheus Metrics

Exposed at `/metrics`:

- `pool_available{pool="poc-agent"}` — gauge
- `pool_claimed{pool="poc-agent"}` — gauge
- `pool_warming{pool="poc-agent"}` — gauge
- `pool_claim_total{pool="poc-agent"}` — counter
- `pool_claim_duration_seconds{pool="poc-agent"}` — histogram
- `pool_exhausted_total{pool="poc-agent"}` — counter (503s)

## Pool Configuration (CRD)

```yaml
apiVersion: agentic.example.com/v1alpha1
kind: ExecutorPool
metadata:
  name: poc-agent
  namespace: agentic-platform
spec:
  # How many idle pods to keep warm
  desired: 5

  # Lease TTL — executor must renew within this window.
  # If it doesn't, claim expires and pod is deleted.
  leaseTTL: 30s

  # How long a pod can stay in warming before being
  # considered stuck and deleted.
  warmingTimeout: 5m

  # Pod template for executor pods in this pool
  podTemplate:
    metadata:
      labels:
        app: executor
    spec:
      initContainers:
        - name: rootfs
          image: poc-agent-rootfs:latest
          command: ["cp", "/rootfs.ext4", "/out/rootfs.ext4"]
          volumeMounts:
            - name: guest-rootfs
              mountPath: /out
      containers:
        - name: executor
          image: executor:latest
          ports:
            - containerPort: 9090
              name: http
          env:
            - name: LISTEN_ADDR
              value: ":9090"
            - name: POOL_OPERATOR_ADDR
              value: "pool-operator.agentic-platform.svc.cluster.local:8080"
            - name: POD_IP
              valueFrom:
                fieldRef:
                  fieldPath: status.podIP
          resources:
            requests:
              cpu: 250m
              memory: 512Mi
            limits:
              cpu: "1"
              memory: 1Gi
          securityContext:
            capabilities:
              add: [NET_ADMIN, NET_RAW, SYS_ADMIN]
          volumeMounts:
            - name: guest-rootfs
              mountPath: /opt/firecracker/rootfs.ext4
              subPath: rootfs.ext4
              readOnly: true
            - name: dev-kvm
              mountPath: /dev/kvm
      volumes:
        - name: guest-rootfs
          emptyDir: {}
        - name: dev-kvm
          hostPath:
            path: /dev/kvm
```

Status subresource (managed by operator):

```yaml
status:
  available: 3
  claimed: 1
  warming: 1
  conditions:
    - type: PoolReady
      status: "True"
      message: "3 pods available"
```

## Internal Architecture

### Components

```
┌─────────────────────────────────────────────────────────────┐
│                      Pool Operator                           │
│                                                              │
│  ┌─────────────┐  ┌─────────────┐  ┌──────────────────────┐│
│  │  HTTP Server │  │ Pool Manager│  │   Pod Informer       ││
│  │             │  │             │  │                      ││
│  │ /claim      │  │ Per-pool    │  │ Watches pods with    ││
│  │ /renew      │  │ reconcile   │  │ pool label           ││
│  │ /release    │  │ loop (5s)   │  │                      ││
│  │ /status     │  │             │  │ Ready → promote      ││
│  │ /metrics    │  │ Scale up/   │  │ Deleted → remove     ││
│  │             │  │ scale down  │  │ Failed → remove      ││
│  └──────┬──────┘  └──────┬──────┘  └──────────┬───────────┘│
│         │                │                     │            │
│         ▼                ▼                     ▼            │
│  ┌──────────────────────────────────────────────────────┐  │
│  │                   Pool State                          │  │
│  │                                                       │  │
│  │  pools: map[string]*Pool  (keyed by pool name)        │  │
│  │                                                       │  │
│  │  Pool {                                               │  │
│  │    mu          sync.Mutex                             │  │
│  │    desired     int                                    │  │
│  │    leaseTTL    time.Duration                          │  │
│  │    available   []PodInfo                              │  │
│  │    claimed     map[string]Claim  (keyed by claim ID)  │  │
│  │    warming     map[string]bool   (keyed by pod name)  │  │
│  │    podTemplate corev1.PodTemplateSpec                 │  │
│  │  }                                                    │  │
│  └──────────────────────────────────────────────────────┘  │
│                                                              │
│  ┌──────────────────────────────────────────────────────┐  │
│  │              ExecutorPool CR Watcher                   │  │
│  │  Watches ExecutorPool CRDs → creates/updates Pool     │  │
│  │  entries in the pools map                             │  │
│  └──────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

### Per-Pool Mutex

Each pool gets its own `sync.Mutex`. A claim against pool "poc-agent" does not block a claim against pool "code-runner". Contention within a single pool is ~100ns (slice pop + map insert).

### Component Interactions

| Component | Reads | Writes |
|---|---|---|
| HTTP `/claim` | `available` | pops from `available`, inserts into `claimed` |
| HTTP `/renew` | `claimed` | updates `ExpiresAt` on claim |
| HTTP `/release` | `claimed` | deletes from `claimed`, appends to `available` |
| Pool Manager | `available`, `warming`, `desired` | creates pods (→ `warming`), deletes pods (→ removes from `available`) |
| Pod Informer | `warming` | promotes `warming` → `available`, removes dead pods from any set |
| CR Watcher | — | updates `desired`, `leaseTTL`, `podTemplate` |

### Pod Labels

The operator labels every pod it creates:

```yaml
labels:
  agentic.example.com/pool: "poc-agent"
  agentic.example.com/status: "warming"    # warming | available | claimed
  agentic.example.com/claim-id: ""         # set when claimed
```

Labels serve two purposes:
1. **Informer filtering** — operator only watches pods with the `pool` label.
2. **State rebuild** — on leader failover, in-memory state is reconstructed from labels.

Labels are updated **asynchronously** after in-memory state changes. In-memory state is authoritative during normal operation.

### State Transitions

```
Operator creates pod
         │
    ┌────▼────┐
    │ WARMING  │  Pod exists, not yet Ready
    └────┬────┘
         │  Informer: pod Ready condition = True
    ┌────▼────┐
    │AVAILABLE │  In the pool, can be claimed
    └────┬────┘
         │  POST /claim
    ┌────▼────┐
    │ CLAIMED  │  Lease active, executor renewing
    └────┬────┘
         │  POST /release
    ┌────▼────┐
    │AVAILABLE │  Back in pool, immediately claimable
    └────┬────┘
         │  ...next claim...
```

## Pool Management

The pool manager runs a reconcile loop every 5 seconds per pool.

### Scale Up

```
supply = len(available) + len(warming)
deficit = desired - supply

if deficit > 0:
    create `deficit` new pods from podTemplate
    add to warming set
```

`desired` governs the **available pool size**, not total pods. If 3 pods are claimed, the pool manager still targets `desired` available pods. Claims trigger automatic backfill.

### Scale Down

```
excess = supply - desired

if excess > 0:
    delete min(excess, len(available)) pods from available
    never delete from claimed or warming
```

### Release Behavior

When a pod is released:
- If `available + warming < desired` → return pod to available (reuse it).
- If pool is already at or above desired → return pod to available anyway. Let the scale-down trim excess on the next reconcile cycle. This avoids deleting a pod that might be claimed again in seconds.

### Dynamic Resize

Updating `spec.desired` on the ExecutorPool CR triggers the CR watcher, which updates the pool's desired count. The pool manager picks up the change on the next reconcile cycle (≤5 seconds) and scales up or down accordingly.

## Safety Nets

### 1. Lease Expiry (executor hung or crashed without releasing)

Every claim has an `ExpiresAt` timestamp, refreshed by `/renew`.

A sweep runs every 30 seconds. For each expired claim:
- Delete the pod (don't return to available — the executor is unresponsive and shouldn't be trusted).
- Pool manager sees the deficit and creates a replacement.

### 2. Warming Timeout (pod stuck starting)

If a pod has been in the warming set longer than `spec.warmingTimeout` (default 5 minutes):
- Check actual pod status via K8s API.
- If Failed, CrashLoopBackOff, or ImagePullBackOff: delete pod, remove from warming.
- Pool manager creates a replacement.

### 3. Informer (pod dies)

Pod deletion or failure is detected immediately by the informer:
- Remove from whichever set (available, claimed, warming).
- Pool manager backfills on next cycle.

### 4. Combined Timeline

```
t=0       Claim granted, leaseTTL=30s
t=10s     Executor renews → ExpiresAt extended
t=20s     Executor renews → ExpiresAt extended
t=25s     Executor hangs (stops renewing)
t=55s     Lease expired (30s since last renewal)
          → Sweep deletes pod, pool manager replaces

If pod crashes instead of hanging:
t=25s     Pod dies → informer fires immediately
          → Removed from claimed, pool manager replaces
          → Detection in seconds, not minutes
```

## Leader Election and Failover

### Startup Sequence

1. Binary starts.
2. controller-runtime manager starts with leader election (K8s Lease).
3. On becoming leader:
   a. List all ExecutorPool CRDs → create Pool entries in memory.
   b. List all pods with `agentic.example.com/pool` label.
   c. Rebuild in-memory state from pod labels:
      - `status=available` → available
      - `status=claimed` → claimed (with claim-id from label)
      - `status=warming` or pod not Ready → warming
   d. Start HTTP server (readiness probe passes).
   e. Start pod informer.
   f. Start pool manager reconcile loops.
   g. Start lease expiry sweep.
4. Serving requests.

### Leader Election Tuning

```go
ctrl.Options{
    LeaseDuration: 5 * time.Second,
    RenewDeadline: 3 * time.Second,
    RetryPeriod:   1 * time.Second,
}
```

Failover window: ~3-5 seconds. During this window, claims and releases fail. Callers retry.

### Stale Label Recovery

Because labels are written asynchronously, they may be slightly stale after failover:

**Pod labeled "available" but was actually claimed:**
The pod ends up in the available pool. If claimed again, the executor rejects the new request (it's still occupied). The new claim fails at the executor level, caller retries and gets a different pod. Original execution finishes, executor calls `/release`. Self-healing.

**Pod labeled "claimed" but was actually released:**
The pod sits in the claimed set with a stale lease. The lease expiry sweep deletes it within leaseTTL. Pool manager replaces it. Self-healing — costs one pod being temporarily unavailable.

## CR Deletion Behavior

When an `ExecutorPool` CR is deleted:
- A **finalizer** ensures cleanup completes before the CR disappears.
- Claimed pods are **not** deleted — running executions finish naturally.
- Available and warming pods are deleted.
- No new claims are accepted for this pool.
- Once all claimed pods have released (or their leases expire), the finalizer is removed and the CR is garbage collected.

## Package Layout

```
agentic-platform/pool-operator/
├── cmd/
│   └── operator/
│       └── main.go                     # Entry point, leader election, wiring
│
├── internal/
│   ├── pool/
│   │   ├── pool.go                     # Pool struct, Claim(), Renew(), Release()
│   │   ├── pool_test.go                # Unit tests
│   │   └── manager.go                  # Reconcile loop, scale up/down, sweeps
│   │
│   ├── server/
│   │   ├── server.go                   # HTTP handlers
│   │   ├── server_test.go              # Handler tests
│   │   └── metrics.go                  # Prometheus metrics
│   │
│   ├── informer/
│   │   ├── pod_watcher.go              # Pod informer event handlers
│   │   └── pod_watcher_test.go
│   │
│   └── controller/
│       ├── executorpool_controller.go   # ExecutorPool CR reconciler
│       └── executorpool_controller_test.go
│
├── api/
│   └── v1alpha1/
│       ├── types.go                    # ExecutorPool CRD types
│       ├── groupversion_info.go        # GVR registration
│       └── zz_generated.deepcopy.go
│
├── Dockerfile
├── go.mod
├── go.sum
└── Makefile
```

### Key Design Principle

`pool/pool.go` is pure Go with no K8s dependencies. The `Pool` struct operates on slices and maps, testable with plain unit tests. K8s interactions (pod creation, label patching, informer events) live in `manager.go`, `pod_watcher.go`, and the controller — injected as interfaces for testability.

## Executor Contract

The executor's responsibilities are minimal:

1. **On receiving work** (waypoint sends `/run`): start a goroutine that calls `POST /renew` to the operator every `leaseTTL / 3`.
2. **On execution complete**: call `POST /release` with the `claim_id`. Best-effort with 3 retries (1s/2s/4s backoff). Do not block on failure — the lease expiry is the safety net.
3. **On `/release` failure after all retries**: log a warning and move on. The operator will detect the expired lease and delete the pod.

The executor does NOT need to: register, heartbeat outside of claims, deregister, or manage pool state in any way.
