# Multi-Cluster Architecture Design

**Goal:** Rearchitect the agentic-platform from a single EKS cluster to a multi-cluster topology that separates platform control-plane services, observability, ingress, and tenant workloads into dedicated clusters — connected via Istio 1.29 ambient mesh and AWS Transit Gateway.

**Status:** Design — pending implementation plan

---

## 1. Cluster Topology

Six clusters total. Five participate in the Istio mesh; the management cluster sits outside.

| Cluster | Network Label | In Mesh | Purpose |
|---------|--------------|---------|---------|
| **management** | — (not in mesh) | No | Cluster lifecycle (CAPA), root CA (cert-manager). ArgoCD later. |
| **control-plane** | `network-cp` | Yes | Platform services: auth, agent registry, memory, platform API |
| **gateway** | `network-gw` | Yes | North-south ingress (agentgateway-proxy, NLB-backed) |
| **observability** | `network-obs` | Yes | Metrics (VictoriaMetrics), traces (Langfuse+ClickHouse), dashboards (Kiali, Grafana) |
| **cell-1** | `network-cell-1` | Yes | Tenant workloads: agents, MCP servers, sandboxes, waypoints |
| **cell-2** | `network-cell-2` | Yes | Tenant workloads: agents, MCP servers, sandboxes, waypoints |

### Management Cluster

Outside the mesh. Provisions and manages all other clusters.

| Component | Purpose |
|-----------|---------|
| Cluster API (CAPA provider) | EKS cluster lifecycle — create, upgrade, scale node groups |
| cert-manager | Root CA issuer. Issues intermediate CAs for each managed cluster's istiod |
| (ArgoCD — future) | GitOps delivery to all managed clusters |

### Control-Plane Cluster

Platform services that all clusters depend on.

| Component | Namespace | Purpose |
|-----------|-----------|---------|
| istiod (1.29) | istio-system | Mesh control plane (ambient profile) |
| ztunnel | istio-system | L4 mTLS (DaemonSet) |
| East-west gateway | istio-system | Inbound HBONE from other clusters |
| Keycloak | keycloak | OIDC/OAuth2, organizations, token exchange |
| OpenFGA | openfga | Fine-grained authorization (per-tenant stores) |
| Platform API | platform-api | Tenant management, cell assignment |
| AgentRegistry | agentregistry | Cross-cluster agent/MCP/skill discovery |
| EverMemOS | evermemos | Long-term memory (MongoDB, Elasticsearch, Milvus, Redis) |
| agentic-operator* | TBD | Tenant namespace provisioning (separate design) |
| OTel Agent + Gateway | otel-system | Local telemetry → obs cluster |
| vmagent | monitoring | Local metrics → VictoriaMetrics |

### Gateway Cluster

Dedicated north-south ingress, decoupled from platform service scaling.

| Component | Namespace | Purpose |
|-----------|-----------|---------|
| istiod (1.29) | istio-system | Mesh control plane |
| ztunnel | istio-system | L4 mTLS |
| East-west gateway | istio-system | Cross-cluster mesh connectivity |
| agentgateway-proxy | agentgateway-system | Ingress gateway (NLB-backed, HTTPRoutes) |
| OTel Agent + Gateway | otel-system | Local telemetry → obs cluster |
| vmagent | monitoring | Local metrics → VictoriaMetrics |

### Observability Cluster

Centralized observability stack. All clusters export metrics and traces here.

| Component | Namespace | Purpose |
|-----------|-----------|---------|
| istiod (1.29) | istio-system | Mesh control plane |
| ztunnel | istio-system | L4 mTLS |
| East-west gateway | istio-system | Inbound HBONE for metric/trace ingestion |
| VictoriaMetrics | monitoring | Single-node. Receives remote-write from all vmagents. Query endpoint for Kiali + Grafana. |
| Langfuse | langfuse | OTLP HTTP trace ingest from all OTel gateways |
| ClickHouse | langfuse | Trace/event analytics storage for Langfuse |
| Kiali | kiali-operator | Multi-cluster mesh visualization. Queries VictoriaMetrics, remote secrets for API access. |
| Grafana | monitoring | Dashboards. Datasource: VictoriaMetrics. |
| OTel Agent + Gateway | otel-system | Local telemetry (self-monitoring) |
| vmagent | monitoring | Local metrics (self-monitoring) |

### Cell Clusters (cell-1, cell-2)

Tenant workloads. Each cell is identical in structure; tenants are pinned to a single cell.

| Component | Namespace | Purpose |
|-----------|-----------|---------|
| istiod (1.29) | istio-system | Mesh control plane |
| ztunnel | istio-system | L4 mTLS |
| East-west gateway | istio-system | Cross-cluster mesh connectivity |
| kagent | kagent-system | Agent CRD reconciliation |
| kmcp | kagent-system | MCP server pod lifecycle |
| openfga-envoy | openfga | Ext_authz adapter (one Deployment per cell, shared by all tenants). Calls OpenFGA in control-plane over mesh. |
| OTel Agent + Gateway | otel-system | Local telemetry → obs cluster |
| vmagent | monitoring | Local metrics → VictoriaMetrics |
| **Per-tenant namespace:** | tenant-{name} | Agent pods, MCP servers, sandboxes, agentgateway waypoint |

---

## 2. Node Groups

### Management Cluster

| Node Group | Instance | Scaling | Workloads |
|-----------|----------|---------|-----------|
| `management` | t3.medium | Fixed 2 | CAPA controllers, cert-manager |

### Control-Plane Cluster

| Node Group | Instance | Scaling | Workloads |
|-----------|----------|---------|-----------|
| `platform` | t3.large | 2-4 | All control-plane services |

### Gateway Cluster

| Node Group | Instance | Scaling | Workloads |
|-----------|----------|---------|-----------|
| `gateway` | t3.medium | 1-3 | agentgateway-proxy, istiod, ztunnel |

### Observability Cluster

| Node Group | Instance | Scaling | Workloads |
|-----------|----------|---------|-----------|
| `obs` | t3.large | 2-4 | VictoriaMetrics, Langfuse, ClickHouse, Kiali, Grafana |

### Cell Clusters (each)

| Node Group | Instance | Scaling | Taint | Workloads |
|-----------|----------|---------|-------|-----------|
| `workload` | t3.large | 1-10, autoscaled | `role=workload:NoSchedule` | Agent pods, MCP servers, sandboxes |
| `waypoint` | t3.small | 1-5, autoscaled | `role=waypoint:NoSchedule` | Per-tenant agentgateway waypoints |
| `gateway` | t3.medium | Fixed 1-2 | `role=gateway:NoSchedule` | East-west gateway |

---

## 3. Networking

### AWS Transit Gateway

All 6 VPCs attach to a single Transit Gateway in us-east-1. Each cluster gets its own VPC with a unique CIDR. Pod CIDRs can overlap since Istio multi-network tunnels all cross-cluster traffic through E-W gateways.

| Cluster | VPC CIDR |
|---------|----------|
| management | 10.0.0.0/16 |
| control-plane | 10.1.0.0/16 |
| gateway | 10.2.0.0/16 |
| observability | 10.3.0.0/16 |
| cell-1 | 10.4.0.0/16 |
| cell-2 | 10.5.0.0/16 |

**TGW route table:** Routes each VPC CIDR to its attachment.

**Security groups:** Allow TCP 15008 (HBONE), TCP 15012 (istiod xDS for remote secrets), TCP 443 (kube API for CAPA and Kiali remote access) between VPCs.

**Management cluster:** Only needs outbound to kube API (443) on all managed clusters. No inbound mesh traffic.

### Istio Multi-Network Configuration

Each in-mesh cluster:
- Labeled `topology.istio.io/network: <network-label>`
- Runs its own istiod (multi-primary model)
- Has an east-west gateway on port 15008 (`protocol: HBONE`, `TLS.Mode: Passthrough`)
- Has remote secrets (kubeconfigs) for all other in-mesh clusters

**Remote secrets** (5 clusters × 4 remote secrets each = 20 total):
- Each istiod uses remote secrets to discover services and endpoints in other clusters
- istiod also auto-discovers E-W gateway IPs from the remote cluster's Gateway Service via the remote secret API access — no static meshNetworks configuration needed
- Each E-W gateway gets a `LoadBalancer` Service with a private IP routable via TGW

**Remote secret RBAC:** Each remote secret kubeconfig uses a dedicated ServiceAccount per remote cluster with read-only permissions: `get`/`list`/`watch` on Services, Endpoints, Pods, Namespaces, and Istio CRDs. Created by `istioctl create-remote-secret` or equivalent automation.

**Remote secret lifecycle:** Initially created by deployment scripts during cluster bootstrap. Rotated by re-running the creation script (kubeconfigs use long-lived SA tokens). Future: managed by ArgoCD with external-secrets-operator for automated rotation.

### Istio Feature Flags

`AMBIENT_ENABLE_BAGGAGE` enables cross-network peer metadata exchange via baggage headers on HBONE CONNECT requests. Required for telemetry attribution across E-W gateways.

**Note:** The exact configuration path for this flag (ztunnel env var vs. meshConfig) should be verified against Istio 1.29 release notes during implementation. It may be set as an environment variable on the ztunnel DaemonSet rather than via meshConfig.

---

## 4. Traffic Flows

### North-South: External → Agent (Cell)

```
External Client
  → AWS NLB (gateway cluster)
    → agentgateway-proxy (ingress gateway)
      → HTTPRoute match (/a2a/{ns}/{agent}, /mcp/{ns}/{server}, etc.)
        → [double HBONE] → Cell E-W gateway
          → dest ztunnel → tenant waypoint (L7 policy)
            → Agent pod
```

The ingress gateway is an L7 proxy originating connections to a remote network, so it creates **double HBONE**: an inner tunnel (ingress ↔ dest ztunnel identity, end-to-end) wrapped in an outer tunnel (ingress ↔ E-W gateway identity). The destination cluster's E-W gateway terminates the outer layer and forwards the inner HBONE as raw bytes to the local ztunnel, which routes through the tenant waypoint for L7 policy.

Only the **destination cluster's** E-W gateway is in the path. The ingress gateway itself acts as the source-side tunnel endpoint — there is no separate source E-W gateway hop.

### East-West: Agent (Cell) → Platform Service (Control-Plane)

```
Agent pod (cell)
  → source ztunnel
    → [single HBONE] → Control-plane E-W gateway
      → dest ztunnel
        → Platform service (EverMemOS, OpenFGA, AgentRegistry, etc.)
```

Standard L4 ztunnel-to-ztunnel path. The source ztunnel connects directly to the remote E-W gateway — no source-side E-W gateway in the path. Single HBONE because ztunnel is an L4 proxy (not L7). Platform services do not have waypoints.

### East-West: Agent (Cell 1) → Agent (Cell 2)

```
Agent pod (Cell 1)
  → source ztunnel
    → [single HBONE] → Cell 2 E-W gateway
      → dest ztunnel
        → tenant waypoint (L7 policy)
          → Agent pod (Cell 2)
```

Source ztunnel connects directly to the destination cell's E-W gateway. The destination ztunnel sees the tenant namespace has a waypoint and routes through it for L7 policy enforcement.

### East-West: Cell → Observability (Telemetry Export)

```
OTel Gateway (cell) → [single HBONE] → Obs E-W gateway → Langfuse
vmagent (cell)      → [single HBONE] → Obs E-W gateway → VictoriaMetrics
```

Telemetry export uses the same mesh routing as any other east-west call.

### Double HBONE — When and Why

Double HBONE occurs only when an **L7 proxy** (ingress gateway or waypoint) originates a connection to a remote network. L7 proxies terminate the original connection and create a new one, so they need to establish both identity layers:

| Scenario | HBONE Type | Why |
|----------|-----------|-----|
| Ingress → remote cell agent | Double | Ingress is L7, creates both layers |
| ztunnel → remote service | Single | ztunnel is L4, single hop to remote E-W GW |
| Waypoint → remote endpoint (multi-cluster LB) | Double | Waypoint is L7, selected a remote backend |

Istio uses internal metadata to signal that L7 policy was already applied by a waypoint, preventing double-enforcement at the destination. (The exact mechanism — HBONE connection metadata, filter state, or headers — should be verified against Istio 1.29 source during implementation.)

In our architecture, double HBONE is primarily the north-south ingress path. East-west agent traffic uses single HBONE since ztunnels are L4.

---

## 5. Service Discovery & Global Services

In Istio multi-primary multi-network, services are discoverable cross-cluster by default via remote secrets — each istiod reads services from all remote clusters. To restrict visibility (rather than enable it), Istio provides the `ServiceScope` API.

For our architecture, we use a default-deny approach: only explicitly labeled services are global. The `ServiceScope` resource is configured to require the `istio.io/global: "true"` label for cross-cluster export.

### Global Services

**Control-plane cluster → discoverable from cells + gateway:**
- `evermemos-app.evermemos`
- `openfga.openfga`
- `keycloak.keycloak`
- `agentregistry.agentregistry`
- `platform-api.platform-api`

**Observability cluster → discoverable from all in-mesh clusters:**
- `langfuse-web.langfuse` (OTLP ingest)
- `victoriametrics.monitoring` (remote-write target)

**Cell clusters → discoverable from gateway cluster (for ingress routing):**
- All tenant agent services
- All tenant MCP server services
- Global label applied at namespace creation (by agentic-operator or bootstrap script)

### Cluster-Local Services (not global)

- istiod, ztunnel — per-cluster mesh infra
- kagent, kmcp — per-cell agent control plane
- OTel agent/gateway, vmagent — local collection, exports out-of-cluster
- ClickHouse — co-located with Langfuse, not directly accessed cross-cluster

### Waypoint Synchronization

Istio requires waypoint configurations to be uniform across clusters. Since tenants are cell-pinned (each tenant namespace exists in exactly one cell), this is naturally satisfied — no waypoint config conflicts.

---

## 6. Observability

### Metrics: vmagent → VictoriaMetrics

Each cluster runs vmagent (Deployment) that scrapes local targets and remote-writes to VictoriaMetrics in the observability cluster.

**Cluster label injection:**
- **ztunnel metrics:** Already include `cluster` label natively (Istio 1.29 ambient). No injection needed.
- **All other metrics** (kube-state-metrics, node-exporter, app pods): Cluster label injected by vmagent via `externalLabels`:

```yaml
spec:
  externalLabels:
    cluster: "cell-1"  # set per-cluster
```

Or via remote-write relabeling:

```yaml
remoteWrite:
  - url: "http://victoriametrics.monitoring.svc:8428/api/v1/write"
    writeRelabelConfigs:
      - targetLabel: cluster
        replacement: "cell-1"
```

**VictoriaMetrics** runs as a single-node instance (`victoria-metrics-single`) in the observability cluster. Single binary, single endpoint. Upgrade to cluster mode (vminsert/vmselect/vmstorage) when scale demands it.

**Retention:** 15 days for metrics (sufficient for dashboards and alerting). Sizing: 50Gi PVC initial, monitor and expand as needed.

### Traces: OTel → Langfuse

Per-cluster OTel pipeline (Agent + Gateway pattern):

| Component | Kind | Config |
|-----------|------|--------|
| OTel Agent | DaemonSet | OTLP receiver (gRPC:4317), `k8sattributes` processor (pod/ns/deploy metadata), `resource` processor (sets `k8s.cluster.name`), `batch` processor, exports to in-cluster gateway |
| OTel Gateway | Deployment (2 replicas) | OTLP receiver, `memory_limiter`, `batch`, exports OTLP HTTP to Langfuse in obs cluster |

OTel collectors run in the `otel-system` namespace (separate from Langfuse lifecycle).

**Cross-cluster trace correlation** works automatically:
- W3C `traceparent` headers propagate through HBONE tunnels (E-W gateways forward headers transparently)
- `AMBIENT_ENABLE_BAGGAGE` adds peer metadata for ztunnel/waypoint-level telemetry
- All OTel gateways export to the same Langfuse → spans with same trace ID reassemble
- `k8s.cluster.name` resource attribute identifies which cluster each span originated in

**Trace retention:** 30 days in ClickHouse. ClickHouse storage: 100Gi PVC initial (2 replicas + 3 Keeper nodes, same as current config).

### Dashboards

**Kiali** (observability cluster, multi-cluster mode):
- Queries VictoriaMetrics for traffic metrics (single endpoint, cluster labels already present)
- Remote secrets for each in-mesh cluster's API server (reads Istio config, k8s resources, workload health)
- Kiali Operator on obs cluster (full install). On each remote cluster, create Kiali SA + RBAC via the Kiali Operator's remote-cluster-only mode or the `kiali-prepare-remote-cluster.sh` script. (Verify exact mechanism against current Kiali Operator docs during implementation.)
- Authentication: anonymous (internal access only)

**Grafana** (observability cluster):
- Datasource: VictoriaMetrics
- Dashboards for all clusters, filterable by `cluster` label

### HA Considerations

For this initial deployment, all observability components run single-replica (except ClickHouse which already has 2 replicas). This is acceptable for dev/staging. For production:
- VictoriaMetrics: upgrade to cluster mode (vminsert/vmselect/vmstorage with replication)
- Langfuse: 2+ replicas behind a Service
- Kiali/Grafana: stateless, easily scaled to 2+ replicas

---

## 7. Certificate Management & Trust

### Architecture

```
Management Cluster (cert-manager)
  Root CA (self-signed ClusterIssuer)
    ├── Intermediate CA: control-plane → istiod signs SPIFFE certs
    ├── Intermediate CA: gateway       → istiod signs SPIFFE certs
    ├── Intermediate CA: observability  → istiod signs SPIFFE certs
    ├── Intermediate CA: cell-1        → istiod signs SPIFFE certs
    └── Intermediate CA: cell-2        → istiod signs SPIFFE certs
```

### How It Works

1. cert-manager runs in the management cluster with a self-signed `ClusterIssuer` as root CA (software-backed; migrate to ACM PCA later as a hardening step — not a one-way door)
2. For each in-mesh cluster, cert-manager issues an intermediate CA `Certificate`
3. Intermediate CA cert+key is distributed to each cluster's `istio-system` namespace as a `cacerts` Secret (Istio plugged-in CA)
4. Each cluster's istiod uses its intermediate CA to sign workload SPIFFE certificates
5. All workload certs chain to the same root → cross-cluster mTLS succeeds

### Intermediate CA Distribution

The `cacerts` secret must get from the management cluster to each managed cluster. Mechanism:

- **Initial bootstrap:** A deployment script runs `kubectl` against the management cluster to extract the intermediate CA cert+key, then applies the `cacerts` Secret to the target cluster's `istio-system` namespace. This runs before Istio installation.
- **Rotation:** cert-manager auto-renews the intermediate CA Certificate. A CronJob or controller in the management cluster detects renewal and pushes the updated secret to managed clusters. (Future: external-secrets-operator or ArgoCD for fully automated sync.)

### Trust Domain

All clusters share `cluster.local` as the SPIFFE trust domain. Workload identity format: `spiffe://cluster.local/ns/{namespace}/sa/{service-account}`.

### Rotation

cert-manager handles intermediate CA rotation automatically. Workload certs are short-lived (24h default) and rotate naturally via istiod.

---

## 8. Authentication & Authorization

### Keycloak (Control-Plane Cluster)

No changes to Keycloak configuration. Same realm (`agents`), same organization-based multi-tenancy, same token exchange flow. Keycloak service is marked global so all clusters can reach it for token validation.

### OpenFGA (Control-Plane Cluster)

OpenFGA server stays in the control-plane cluster. Per-cell openfga-envoy adapters call it over the mesh. One openfga-envoy Deployment per cell cluster, in the `openfga` namespace, shared by all tenants in that cell. The ext_authz flow:

```
Agent request → tenant waypoint (cell, tenant-{name} ns)
  → ext_authz to openfga-envoy (cell, openfga ns)
    → OpenFGA gRPC (control-plane, via mesh) → allow/deny
```

### Ingress Authentication

AgentgatewayPolicy on the ingress gateway (gateway cluster):
- JWT validation via Keycloak JWKS (cross-cluster call to control-plane)
- Required claim: `organization` (tenant gate)
- Audience validation deferred to per-tenant waypoints

---

## 9. Cluster Provisioning

### Cluster API (Management Cluster)

The management cluster runs Cluster API with the CAPA (Cluster API Provider AWS) provider for EKS cluster lifecycle management. CAPA replaces the current `eksctl`-based `00-create-cluster.sh` for cluster creation and node group management.

Each managed cluster is defined as a set of CAPA CRs:
- `AWSManagedControlPlane` — EKS control plane config (k8s version, IAM, networking)
- `MachinePool` / `AWSManagedMachinePool` — Node groups with instance types, scaling, taints

### Deployment Scripts

The existing numbered scripts (`00` through `07`) are repurposed for **application-level deployment** within each cluster (Helm releases, manifests, secrets). Cluster creation is handled by CAPA, not scripts.

Scripts gain a `--cluster-type` parameter to deploy the right components to the right cluster:

```bash
# CAPA creates the cluster (declarative, via management cluster)
# Then application-level deployment:
./scripts/03-deploy-platform.sh --cluster-type control-plane
./scripts/03-deploy-platform.sh --cluster-type cell --cluster-name cell-1
./scripts/03-deploy-platform.sh --cluster-type observability
```

### Cell Assignment

When a new tenant is onboarded, the Platform API (or agentic-operator — separate design) selects a cell and creates the tenant namespace there. The interface between this spec and the agentic-operator spec is:

- **Input:** Platform API receives tenant creation request (org name, config)
- **Decision:** Platform API selects a cell (initially round-robin or least-loaded)
- **Action:** Platform API (or operator) creates namespace in the selected cell with required labels (`istio.io/global: "true"`, ambient enrollment, waypoint config)
- **Record:** Cell assignment is stored in the Platform API's database (immutable — tenants don't move between cells)

The exact orchestration mechanism (direct API call vs. CR reconciliation) is deferred to the agentic-operator design.

---

## 10. Migration from Single-Cluster

This design replaces the current single-cluster deployment. Key changes:

| Current (single-cluster) | New (multi-cluster) |
|--------------------------|---------------------|
| 1 EKS cluster, 3 node groups | 6 EKS clusters, managed by CAPA |
| Istio 1.28.3 | Istio 1.29 with AMBIENT_ENABLE_BAGGAGE |
| Prometheus + kube-prometheus-stack | vmagent → VictoriaMetrics (single-node) |
| No cross-cluster metrics | vmagent remote-write with cluster labels |
| Single OTel collector (gRPC→HTTP bridge) | Per-cluster OTel Agent + Gateway pipeline |
| Kiali (single-cluster) | Kiali multi-cluster with remote secrets |
| Kyverno (5 ClusterPolicies) | Removed — replaced by agentic-operator (separate design) |
| Sandbox router (shared) | Removed — per-tenant routing TBD (separate design) |
| Self-signed Istio CA per cluster | cert-manager shared root CA with intermediate CAs |
| eksctl | Cluster API (CAPA) |
| Single VPC | 6 VPCs + Transit Gateway |

### HA Considerations

For this initial deployment, all control-plane components (istiod, Keycloak, OpenFGA, Langfuse, VictoriaMetrics) run single-replica. This is acceptable for dev/staging. Production hardening (multi-replica, pod disruption budgets, cross-AZ scheduling) is a follow-up.

### Out of Scope (Separate Designs)

- **agentic-operator** — tenant namespace provisioning, cell assignment, replaces Kyverno + onboarding scripts
- **Per-tenant sandbox routing** — replaces shared sandbox-router
- **ArgoCD GitOps** — future addition to management cluster
- **External DNS** — production domain setup
- **ACM PCA integration** — hardware-backed root CA (hardening step)

---

## 11. AWS Managed Resources

Shared across clusters via TGW connectivity. All reside in the **control-plane VPC** (10.1.0.0/16). Security groups allow inbound on 5432/6379 from all cluster VPC CIDRs.

| Resource | Type | Shared By |
|----------|------|-----------|
| RDS PostgreSQL 17 | db.t4g.small → db.t4g.medium | Keycloak, OpenFGA, AgentRegistry, Langfuse |
| ElastiCache Redis | cache.t4g.small | Langfuse, EverMemOS |
| S3 | agentic-platform-langfuse-dev | Langfuse |
