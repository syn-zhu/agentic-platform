# Multi-Cluster Architecture Design

**Goal:** Rearchitect the agentic-platform from a single EKS cluster to a multi-cluster topology that separates platform control-plane services, observability, and tenant workloads into dedicated clusters — with independent meshes per cluster and standard HTTPS for cross-cluster communication.

**Status:** Design — pending implementation plan (Phase 2 rework)

---

## 1. Cluster Topology

Four clusters total. Each cluster that runs a mesh has its own independent Istio installation — there is no cross-cluster mesh. Cross-cluster communication uses standard HTTPS over AWS Transit Gateway.

| Cluster | Mesh | Purpose |
|---------|------|---------|
| **control-plane** | Istio sidecar (internal only) | Platform API, Keycloak, OpenFGA, AgentRegistry, Langfuse+ClickHouse, CAPA, N-S ingress |
| **observability** | None | VictoriaMetrics, Grafana, Kiali (per-cell mesh visualization) |
| **cell-1** | Istio ambient (independent) | Tenant workloads, EverMemOS, kagent, own gateway, own waypoints |
| **cell-2** | Istio ambient (independent) | Tenant workloads, EverMemOS, kagent, own gateway, own waypoints |

### Why No Multi-Cluster Mesh

In typical cell-based architectures, control plane and data plane are deliberately separated:
- **Blast radius** — a mesh issue in a cell doesn't affect platform services
- **Upgrade independence** — Istio upgrades per-cell without touching control-plane
- **Failure domains** — each cluster's Istio is fully independent
- **Simpler trust model** — cross-cluster uses standard TLS + API keys, not SPIFFE

This eliminates: east-west gateways, remote secrets, shared CA, ServiceScope, HBONE tunneling between clusters. Each cluster is a self-contained island.

### Control-Plane Cluster

Platform services and cluster management. Istio in sidecar mode for internal service-to-service communication only.

| Component | Namespace | Purpose |
|-----------|-----------|---------|
| istiod (1.29, sidecar mode) | istio-system | Internal service mesh |
| N-S ingress gateway | agentgateway-system | External access to platform services (NLB-backed) |
| Cluster API (CAPA) | capi-system / capa-system | Provisions and manages cell clusters |
| Keycloak | keycloak | OIDC/OAuth2, organizations, token exchange |
| OpenFGA | openfga | Fine-grained authorization (per-tenant stores) |
| Platform API | platform-api | Tenant management, cell assignment |
| AgentRegistry | agentregistry | Cross-cluster agent/MCP/skill discovery |
| Langfuse | langfuse | LLM observability — customer trace data (OTLP ingest) |
| ClickHouse | langfuse | Trace/event analytics storage for Langfuse |
| agentic-operator* | TBD | Tenant namespace provisioning (separate design) |
| OTel Agent + Gateway | otel-system | Local telemetry → Langfuse (local) + VictoriaMetrics (obs) |
| vmagent | monitoring | Local metrics → VictoriaMetrics (obs) |
| (ArgoCD — future) | argocd | GitOps delivery to all clusters |

### Observability Cluster

Platform-level infrastructure metrics and mesh visualization. No mesh — receives data over standard networking.

| Component | Namespace | Purpose |
|-----------|-----------|---------|
| VictoriaMetrics | monitoring | Single-node. Receives remote-write from all vmagents. Query endpoint for Kiali + Grafana. |
| Kiali | kiali-operator | Per-cell mesh visualization. Queries VictoriaMetrics + remote API access to each cell. |
| Grafana | monitoring | Dashboards. Datasource: VictoriaMetrics. |
| vmagent | monitoring | Local metrics (self-monitoring) |

Kiali monitors each cell's ambient mesh individually — no unified cross-cluster graph, but full per-cell service graph visualization by switching between cluster views.

### Cell Clusters (cell-1, cell-2)

Tenant workloads. Each cell is identical in structure with its own independent ambient mesh. Tenants are pinned to a single cell.

| Component | Namespace | Purpose |
|-----------|-----------|---------|
| istiod (1.29, ambient mode) | istio-system | Cell-local ambient mesh |
| ztunnel | istio-system | L4 mTLS (DaemonSet) |
| istio-cni | istio-system | eBPF traffic redirection |
| Cell gateway | agentgateway-system | External access to agents in this cell (NLB-backed) |
| kagent | kagent-system | Agent CRD reconciliation |
| kmcp | kagent-system | MCP server pod lifecycle |
| EverMemOS | evermemos | Long-term memory (per-cell, local) |
| openfga-envoy | openfga | Ext_authz adapter — calls OpenFGA in control-plane over HTTPS |
| OTel Agent + Gateway | otel-system | Local telemetry → Langfuse (control-plane) |
| vmagent | monitoring | Local metrics → VictoriaMetrics (obs) |
| **Per-tenant namespace:** | tenant-{name} | Agent pods, MCP servers, sandboxes, agentgateway waypoint |

---

## 2. Node Groups

### Control-Plane Cluster

| Node Group | Instance | Scaling | Workloads |
|-----------|----------|---------|-----------|
| `platform` | t3.large | 2-4 | All control-plane services |

### Observability Cluster

| Node Group | Instance | Scaling | Workloads |
|-----------|----------|---------|-----------|
| `obs` | t3.large | 2-3 | VictoriaMetrics, Kiali, Grafana |

### Cell Clusters (each)

| Node Group | Instance | Scaling | Taint | Workloads |
|-----------|----------|---------|-------|-----------|
| `workload` | t3.large | 1-10, autoscaled | `role=workload:NoSchedule` | Agent pods, MCP servers, sandboxes |
| `waypoint` | t3.small | 1-5, autoscaled | `role=waypoint:NoSchedule` | Per-tenant agentgateway waypoints |
| `gateway` | t3.medium | Fixed 1-2 | `role=gateway:NoSchedule` | Cell gateway (NLB-backed) |

---

## 3. Networking

### AWS Transit Gateway

All 4 VPCs attach to a single Transit Gateway in us-east-1. Cross-cluster communication (cell → control-plane, cell → obs) uses standard HTTPS routed over TGW. No mesh traffic crosses cluster boundaries.

| Cluster | VPC CIDR |
|---------|----------|
| control-plane | 10.1.0.0/16 |
| observability | 10.2.0.0/16 |
| cell-1 | 10.3.0.0/16 |
| cell-2 | 10.4.0.0/16 |

**TGW route table:** Routes each VPC CIDR to its attachment.

**Security groups:** Allow TCP 443 (HTTPS — control-plane services, kube API for CAPA and Kiali) between VPCs. No mesh ports (15008, 15012) needed cross-cluster.

### Istio Configuration — Control-Plane Cluster

Sidecar mode, internal only. No multi-cluster, no ambient.

```yaml
# istiod values
profile: default  # sidecar mode
```

Namespaces that need mesh: labeled `istio-injection: enabled`. Services communicate via sidecar proxies within the cluster.

### Istio Configuration — Cell Clusters

Ambient mode, cell-local. Each cell has its own independent istiod with its own self-signed CA.

```yaml
# istiod values
profile: ambient
```

No `meshID`, `clusterName`, `network` needed — single-cluster ambient, no multi-cluster configuration. Each istiod generates its own self-signed root CA (the default).

---

## 4. Traffic Flows

### North-South: External → Platform API

```
External Client
  → AWS NLB (control-plane cluster)
    → ingress gateway (control-plane)
      → Platform API / Keycloak / AgentRegistry
```

The control-plane ingress only routes to local platform services. It does NOT route to agents in cells.

### North-South: External → Agent (Cell)

```
External Client
  → AWS NLB (cell cluster)
    → cell gateway (agentgateway-proxy)
      → HTTPRoute match (/a2a/{ns}/{agent}, /mcp/{ns}/{server}, etc.)
        → ztunnel → tenant waypoint (L7 policy)
          → Agent pod
```

Each cell has its own NLB and gateway. The Platform API (or DNS/routing layer) directs clients to the correct cell's endpoint based on tenant-to-cell mapping.

### East-West: Agent (Cell) → Control-Plane Service

```
Agent pod (cell)
  → HTTPS request to control-plane NLB (via TGW)
    → control-plane ingress or NLB
      → Keycloak / OpenFGA / AgentRegistry / Langfuse
```

Standard HTTPS over TGW private networking. No mesh involved. Agents use service URLs configured at deployment time (e.g., `https://cp.internal.agentic.io/...`).

### East-West: Agent (Cell) → EverMemOS

```
Agent pod (cell)
  → ztunnel → EverMemOS pod (same cell, via ambient mesh)
```

Local call within the cell's ambient mesh. No cross-cluster hop.

### Telemetry Flow

```
Traces: Agent → OTel Agent → OTel Gateway → OTLP HTTPS → Langfuse (control-plane)
Metrics: vmagent (per cluster) → remote-write HTTPS → VictoriaMetrics (obs)
```

Both flows use standard HTTPS over TGW. No mesh routing for telemetry export.

### Cross-Cell Agent-to-Agent (A2A)

Not supported directly in this topology. If needed later, options include:
- Route through cell gateways (cell-1 agent → cell-1 gateway → cell-2 gateway → cell-2 agent)
- Platform API mediates the call
- Future: selective mesh peering between cells

---

## 5. Service Discovery

### Within a Cell (Istio ambient)

Standard Kubernetes service discovery + Istio ambient mesh. Services within a cell discover each other via DNS (`svc.cluster.local`). Waypoints provide L7 policy.

### Cross-Cluster (no mesh)

Cells discover control-plane services via configured URLs, not mesh service discovery. These URLs are injected as environment variables or ConfigMaps at deployment time:

- `KEYCLOAK_URL=https://cp-nlb.internal:443/realms/agents`
- `OPENFGA_URL=https://cp-nlb.internal:443/openfga`
- `AGENTREGISTRY_URL=https://cp-nlb.internal:443/registry`
- `LANGFUSE_OTLP_URL=https://cp-nlb.internal:443/otlp`

The Platform API / agentic-operator provisions these URLs when creating tenant namespaces.

### Tenant Agent Discovery

The AgentRegistry in the control-plane cluster serves as the cross-cluster catalog. Agents register themselves at startup; other agents/clients query the registry to find agent endpoints. The registry returns cell-specific gateway URLs.

---

## 6. Observability

### Metrics: vmagent → VictoriaMetrics

Each cluster runs vmagent (Deployment) that scrapes local targets and remote-writes to VictoriaMetrics in the observability cluster over HTTPS via TGW.

**Cluster label injection:**
- **Cell clusters (ztunnel metrics):** Already include `cluster` label natively (Istio 1.29 ambient). No injection needed.
- **All other metrics** (kube-state-metrics, node-exporter, app pods): Cluster label injected by vmagent via `externalLabels`:

```yaml
spec:
  externalLabels:
    cluster: "cell-1"  # set per-cluster
```

**VictoriaMetrics** runs as a single-node instance in the observability cluster.

**Retention:** 15 days for metrics. Sizing: 50Gi PVC initial.

### Traces: OTel → Langfuse

Per-cluster OTel pipeline (Agent + Gateway pattern):

| Component | Kind | Config |
|-----------|------|--------|
| OTel Agent | DaemonSet | OTLP receiver (gRPC:4317), `k8sattributes` processor, `resource` processor (sets `k8s.cluster.name`), exports to in-cluster gateway |
| OTel Gateway | Deployment (2 replicas) | OTLP receiver, `memory_limiter`, `batch`, exports OTLP HTTPS to Langfuse in control-plane cluster |

OTel collectors run in the `otel-system` namespace.

**Cross-cluster trace correlation** works automatically — W3C `traceparent` headers are forwarded by cell gateways. All OTel gateways export to the same Langfuse instance → spans with same trace ID reassemble. `k8s.cluster.name` identifies which cluster each span originated in.

**Trace retention:** 30 days in ClickHouse. Storage: 100Gi PVC initial.

### Dashboards

**Kiali** (observability cluster):
- Queries VictoriaMetrics for traffic metrics (single endpoint, cluster labels present)
- Remote API access to each cell cluster (reads Istio config, k8s resources, workload health)
- Shows each cell's ambient mesh topology independently (switch between cluster views)
- Cannot show unified cross-cluster graph (no multi-cluster mesh)

**Grafana** (observability cluster):
- Datasource: VictoriaMetrics
- Dashboards for all clusters, filterable by `cluster` label

---

## 7. Certificate Management

### No Shared CA

Each cluster manages its own Istio CA independently:
- **Control-plane:** istiod in sidecar mode generates its own self-signed CA for SPIFFE certs
- **Cell clusters:** each istiod in ambient mode generates its own self-signed CA
- **Observability:** no mesh, no CA needed

Cross-cluster communication uses standard TLS (NLB/ALB termination or service-level TLS), not mesh mTLS.

### Future Hardening

If needed later:
- cert-manager per-cluster for TLS cert management (ingress certs, etc.)
- ACM PCA for production-grade CA backing

---

## 8. Authentication & Authorization

### Keycloak (Control-Plane Cluster)

Same realm (`agents`), same organization-based multi-tenancy, same token exchange flow. Exposed via control-plane ingress/NLB with TLS.

### OpenFGA (Control-Plane Cluster)

OpenFGA server in the control-plane cluster. Per-cell openfga-envoy adapters call it over HTTPS via TGW. One openfga-envoy Deployment per cell, in the `openfga` namespace.

```
Agent request → tenant waypoint (cell)
  → ext_authz to openfga-envoy (cell, openfga ns)
    → OpenFGA HTTPS (control-plane, via TGW) → allow/deny
```

### Ingress Authentication

**Control-plane ingress:** JWT validation via Keycloak JWKS.

**Cell gateways:** JWT validation via Keycloak JWKS (cross-cluster HTTPS call to control-plane). Required claim: `organization` (tenant gate). Per-tenant waypoints handle audience validation.

---

## 9. Cluster Provisioning

### Cluster API (Control-Plane Cluster)

CAPA runs in the control-plane cluster (not a separate management cluster). It provisions and manages cell clusters and the observability cluster.

Each managed cluster is defined as CAPA CRs:
- `AWSManagedControlPlane` — EKS control plane config
- `MachinePool` / `AWSManagedMachinePool` — Node groups

### VPC Layout

| Cluster | VPC CIDR |
|---------|----------|
| control-plane | 10.1.0.0/16 |
| observability | 10.2.0.0/16 |
| cell-1 | 10.3.0.0/16 |
| cell-2 | 10.4.0.0/16 |

Pod CIDRs can overlap — no cross-cluster pod routing.

### Transit Gateway

- Single TGW in us-east-1, all 4 VPCs attached
- Security groups: TCP 443 between VPCs (HTTPS for control-plane services, kube API for CAPA/Kiali)
- No mesh ports needed cross-cluster

### Deployment Scripts

Scripts use a `--cluster-type` parameter:

```bash
./scripts/phase2/03-deploy-platform.sh --cluster-type control-plane
./scripts/phase2/03-deploy-platform.sh --cluster-type cell --cluster-name cell-1
```

---

## 10. Migration from Single-Cluster

| Current (single-cluster) | New (multi-cluster) |
|--------------------------|---------------------|
| 1 EKS cluster, 3 node groups | 4 EKS clusters (cp, obs, cell-1, cell-2) |
| Istio 1.28.3 ambient (single mesh) | Istio 1.29: sidecar on control-plane, ambient per-cell (independent) |
| All services co-located | Platform services in control-plane, tenant workloads in cells |
| Single agentgateway ingress | Control-plane ingress + per-cell gateways |
| Centralized EverMemOS | Per-cell EverMemOS |
| Prometheus + kube-prometheus-stack | vmagent → VictoriaMetrics |
| Single OTel collector | Per-cluster OTel Agent + Gateway |
| Kiali (single-cluster) | Kiali per-cell visualization (obs cluster) |
| Kyverno | Removed — replaced by agentic-operator (separate design) |
| Sandbox router (shared) | Removed — per-tenant routing TBD (separate design) |
| eksctl | CAPA (in control-plane cluster) |
| Single VPC | 4 VPCs + Transit Gateway |

### HA Considerations

All components run single-replica initially (dev/staging). Production hardening is a follow-up.

### Out of Scope (Separate Designs)

- **agentic-operator** — tenant namespace provisioning, cell assignment
- **Per-tenant sandbox routing** — replaces shared sandbox-router
- **ArgoCD GitOps** — future addition to control-plane cluster
- **External DNS** — production domain setup
- **Cross-cell A2A routing** — if needed, separate design

---

## 11. AWS Managed Resources

Shared across clusters via TGW. Reside in the **control-plane VPC** (10.1.0.0/16).

| Resource | Type | Used By |
|----------|------|---------|
| RDS PostgreSQL 17 | db.t4g.medium | Keycloak, OpenFGA, AgentRegistry, Langfuse |
| ElastiCache Redis | cache.t4g.small | Langfuse |
| S3 | agentic-platform-langfuse-dev | Langfuse |

Security groups: allow inbound TCP 5432/6379 from all cluster VPC CIDRs.

Note: EverMemOS is per-cell with its own in-cluster stateful storage (MongoDB, Elasticsearch, Milvus). No shared AWS resources needed for EverMemOS.
