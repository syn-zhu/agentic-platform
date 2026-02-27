# Platform

This directory contains the full platform deployment: Helmfile orchestration, Helm value overrides, Kubernetes manifests applied post-install, environment configs, and custom container images.

## Directory Structure

```
platform/
├── helmfile.yaml                          # Orchestrates ~15 Helm releases in phased order
├── environments/
│   ├── defaults.yaml                      # Shared config (cluster name, region, Langfuse endpoint)
│   └── dev.yaml                           # Dev overrides (S3 bucket name)
├── values/                                # Helm chart value overrides (one per release)
│   ├── istiod.yaml
│   ├── istio-cni.yaml
│   ├── ztunnel.yaml
│   ├── kiali.yaml
│   ├── agentgateway-crds.yaml
│   ├── agentgateway.yaml
│   ├── kagent-crds.yaml
│   ├── kagent.yaml
│   ├── clickhouse.yaml
│   ├── langfuse.yaml
│   ├── kube-prometheus-stack.yaml
│   ├── keycloak.yaml
│   └── kyverno.yaml
├── manifests/                             # Post-install Kubernetes manifests
│   ├── namespaces.yaml                    # Platform namespace definitions + labels
│   ├── platform-rbac.yaml                 # tenant-agent-developer ClusterRole
│   ├── agentgateway-proxy.yaml            # Ingress Gateway (port 80, all namespaces)
│   ├── agentgateway-parameters.yaml       # Ingress proxy config (NLB, tracing, gateway nodes)
│   ├── agentgateway-waypoint-parameters.yaml  # Waypoint proxy config (ClusterIP, Istio CA, agent nodes)
│   ├── anthropic-backend.yaml             # Default LLM backend (Claude Sonnet 4)
│   ├── keycloak-jwt-policy.yaml           # JWT validation policy (Keycloak agents realm)
│   ├── keycloak-agents-realm.json         # Keycloak realm import (tenants, OIDC)
│   ├── otel-collector.yaml                # gRPC→HTTP OTLP bridge for Langfuse
│   ├── sandbox-router.yaml                # Dynamic reverse proxy for agent sandboxes
│   ├── sandbox-router-route.yaml          # HTTPRoute for /sandbox/* traffic
│   ├── platform-tools-remotemcpserver.yaml    # Shared tool server (cross-namespace)
│   ├── kyverno-auto-expose.yaml           # Auto-generate HTTPRoutes from annotations
│   ├── kyverno-platform-scheduling.yaml   # Inject platform node scheduling
│   ├── kyverno-tenant-scheduling.yaml     # Inject agent node scheduling
│   ├── evermemos.yaml                     # EverMemOS long-term memory system (all resources)
│   └── evermemos-gateway.yaml             # EverMemOS waypoint Gateway + AgentgatewayPolicy
```

## Helmfile Deployment Phases

`helmfile.yaml` deploys releases in dependency order. All releases use `wait: true` with a 600-second timeout.

### Phase 0 — Istio Ambient Mesh

Installs the zero-sidecar service mesh foundation. All four charts are pinned to Istio **1.28.3**.

| Release | Chart | Purpose |
|---------|-------|---------|
| `istio-base` | `istio/base` | Istio CRDs and cluster-wide resources |
| `istiod` | `istio/istiod` | Control plane (ambient profile, platform nodes) |
| `istio-cni` | `istio/cni` | DaemonSet for transparent traffic redirection (tolerates all taints) |
| `ztunnel` | `istio/ztunnel` | L4 per-node proxy — mTLS, SPIFFE identity, HBONE tunneling |
| `kiali-operator` | `kiali/kiali-operator` | Service mesh observability UI |

### Phase 1 — CRDs

CRDs must exist before their controllers can start.

| Release | Chart | Version |
|---------|-------|---------|
| `agentgateway-crds` | `oci://ghcr.io/kgateway-dev/charts/agentgateway-crds` | 2.3.0-beta.1 |
| `kagent-crds` | `oci://ghcr.io/kagent-dev/kagent/helm/kagent-crds` | 0.7.13 |

### Phase 2 — Observability & Identity

| Release | Chart | Version | Purpose |
|---------|-------|---------|---------|
| `clickhouse` | `bitnami/clickhouse` | 9.4.3 | Analytics database for Langfuse (2 replicas, 3 Keeper nodes) |
| `langfuse` | `langfuse/langfuse` | 1.5.19 | LLM observability — OTEL ingestion, prompt/completion logging |
| `kube-prometheus-stack` | `prometheus-community/kube-prometheus-stack` | — | Prometheus, Grafana, Alertmanager |
| `keycloak` | `bitnami/keycloak` | 25.2.0 | Identity provider — JWT issuance, tenant claims |

### Phase 2b — Policy Engine

| Release | Chart | Version |
|---------|-------|---------|
| `kyverno` | `kyverno/kyverno` | 3.3.4 |

### Phase 3 — Controllers

Both use **patched images** built from `vendor/` forks.

| Release | Chart | Version | Patches |
|---------|-------|---------|---------|
| `agentgateway` | `oci://ghcr.io/kgateway-dev/charts/agentgateway` | 2.3.0-beta.1 | Istio ambient waypoint support |
| `kagent` | `oci://ghcr.io/kagent-dev/kagent/helm/kagent` | 0.7.13 | Trace context propagation + A2A Service discovery |

The kagent release also deploys:
- **kmcp** subchart (v0.2.6) — manages MCPServer pod lifecycles
- **Built-in agents**: k8s-agent, kgateway-agent, observability-agent, promql-agent, helm-agent
- **Tool servers**: kagent-tools (platform tools), grafana-mcp, querydoc

## Namespaces

Defined in `manifests/namespaces.yaml`. Labels control mesh enrollment and Kyverno policy targeting.

| Namespace | Labels | Mesh |
|-----------|--------|------|
| `istio-system` | `component: istio` | — |
| `agentgateway-system` | `component: agentgateway`, `gateway-discovery: true` | — |
| `kagent-system` | `component: kagent`, `gateway-discovery: true` | ambient |
| `langfuse` | `component: langfuse` | ambient |
| `monitoring` | `component: monitoring` | ambient |
| `keycloak` | `component: keycloak` | — |
| `evermemos` | `component: evermemos` | ambient |
| `kyverno` | (managed by Helm) | — |

All labels use the `platform.agentic.io/` prefix. The `istio.io/dataplane-mode: ambient` label enrolls a namespace in the mesh (L4 mTLS via ztunnel). `agentgateway-system` is _not_ enrolled — it hosts the ingress gateway proxy. North-south traffic reaches meshed backends via the `istio.io/ingress-use-waypoint` feature, which routes ingress traffic through tenant waypoints for L7 policy enforcement.

## Post-Install Manifests

These are applied by `scripts/04-post-install.sh` after Helmfile sync. They depend on controllers and CRDs being available.

### Gateway Configuration

**`agentgateway-proxy.yaml`** — The central ingress Gateway on port 80. All HTTPRoutes (auto-generated or manual) attach to its `platform` listener. Runs on gateway nodes behind an AWS NLB.

**`agentgateway-parameters.yaml`** — Configures the ingress proxy: NLB annotations, gateway node scheduling, OTEL tracing to Langfuse with GenAI semantic field mapping, admin UI on port 19000. The `<BASE64_PK_SK>` placeholder is substituted by the post-install script.

**`agentgateway-waypoint-parameters.yaml`** — Configures per-tenant waypoint proxies. Key differences from the ingress proxy:
- **ClusterIP** (not LoadBalancer) — mesh-internal only
- **Istio integration** — points at istiod CA for SPIFFE certs, `trustDomain: cluster.local`
- **`dataplaneMode: none`** — waypoints must NOT be enrolled in ambient (ztunnel's eBPF would intercept port 15008, preventing the waypoint from binding)
- **Agent node scheduling** — co-located with agent pods

Traffic flow through a waypoint:
```
client pod → ztunnel → HBONE:15008 → waypoint → backend pod
```

### LLM Backend

**`anthropic-backend.yaml`** — Default Anthropic backend (Claude Sonnet 4). Annotated with `expose: true` and `expose-alias: default`, so Kyverno generates a route at `/llm/default/anthropic`. The Secret holds the real API key (substituted by post-install script); tenants use dummy keys and the gateway injects the real one.

### Auth Policies

**`keycloak-jwt-policy.yaml`** — JWT validation on the ingress gateway. Verifies tokens against Keycloak's JWKS endpoint, checks issuer and `agent-gateway` audience, and requires a `tenant` claim in the token. Apply only after Keycloak is running.

### Observability

**`otel-collector.yaml`** — Bridges kagent's gRPC OTLP output to Langfuse's HTTP OTLP endpoint. Deployment + Service + ConfigMap in the `langfuse` namespace. Batch processor: 5-second timeout, 512 batch size. AgentGateway sends HTTP OTLP directly to Langfuse and does not use this collector.

### Sandbox Router

**`sandbox-router.yaml`** — A FastAPI reverse proxy that routes requests to sandbox pods based on headers:

| Header | Required | Default | Purpose |
|--------|----------|---------|---------|
| `X-Sandbox-ID` | Yes | — | Sandbox pod/service name |
| `X-Sandbox-Namespace` | No | `default` | Target namespace |
| `X-Sandbox-Port` | No | `8080` | Target port |

The router constructs `{id}.{namespace}.svc.cluster.local:{port}` and proxies the request. Deployed on platform nodes with an init container that installs Python dependencies.

**`sandbox-router-route.yaml`** — HTTPRoute attaching `/sandbox/*` to the ingress gateway, forwarding to the sandbox-router Service.

### Platform RBAC

**`platform-rbac.yaml`** — Defines the `tenant-agent-developer` ClusterRole. Tenants get this bound per-namespace via RoleBinding (created during onboarding). Grants:

| Resource | Verbs |
|----------|-------|
| Agents, RemoteMCPServers, ModelConfigs | Full CRUD |
| MCPServers (kmcp) | Full CRUD |
| AgentgatewayBackends | Full CRUD |
| Secrets, ConfigMaps, Services | Full CRUD |
| HTTPRoutes, GRPCRoutes | Read-only (auto-generated by Kyverno) |
| Pods, Deployments | Read-only |
| Sandboxes, SandboxClaims, SandboxTemplates | Full CRUD |

### Shared Tools

**`platform-tools-remotemcpserver.yaml`** — A `RemoteMCPServer` in `kagent-system` with `allowedNamespaces.from: All`. Points at the kagent-tools server. Any tenant agent can reference `platform-tools` as a cross-namespace tool source.

### EverMemOS (Long-Term Memory)

**`evermemos.yaml`** — Complete deployment of the EverMemOS long-term memory system. A single manifest defines all resources in the `evermemos` namespace:

| Resource | Kind | Purpose |
|----------|------|---------|
| `evermemos-config` | ConfigMap | Application config: endpoints, AI provider settings, tenant mode |
| `evermemos-mongodb` | StatefulSet + Service | Document store (MongoDB 7.0, 20Gi PVC) |
| `evermemos-elasticsearch` | StatefulSet + Service | BM25 full-text search (ES 8.11.0, 30Gi PVC) |
| `evermemos-milvus-etcd` | StatefulSet + Service | Milvus metadata store (etcd 3.5.5, 5Gi PVC) |
| `evermemos-milvus-minio` | StatefulSet + Service | Milvus object storage (MinIO, 20Gi PVC) |
| `evermemos-milvus` | StatefulSet + Service | Vector search (Milvus 2.5.2, HNSW/COSINE 1024 dims, 20Gi PVC) |
| `evermemos-redis` | Deployment + Service | Caching and request tracking (Redis 7.2) |
| `evermemos` | Deployment + Service | REST API on port 1995 (custom ECR image v1.0.1) |

The app Service is labeled `istio.io/use-waypoint: evermemos-waypoint` so agent traffic is routed through the waypoint for tracing. Internal infrastructure services (MongoDB, ES, Milvus, Redis) are not routed through the waypoint.

External AI services (no GPU required):
- **Embedding**: DeepInfra — Qwen3-Embedding-4B (1024 dims)
- **Reranker**: DeepInfra — Qwen3-Reranker-4B
- **LLM**: OpenRouter (configurable model)

Multi-tenant mode is enabled (`TENANT_NON_TENANT_MODE=false`).

**`evermemos-gateway.yaml`** — Deploys an AgentGateway waypoint proxy for the `evermemos` namespace. Provides Langfuse tracing on every memory API call and a 60-second request timeout. Includes an `AgentgatewayPolicy` for timeout configuration.

## Kyverno Policies

Five ClusterPolicies enforce platform conventions automatically. All use the `platform.agentic.io/` annotation and label namespace.

### Generation Policy

**`kyverno-auto-expose.yaml`** — Watches for `platform.agentic.io/expose: "true"` annotations and generates HTTPRoutes:

| Source Resource | Condition | Generated Path |
|-----------------|-----------|----------------|
| `Agent` (kagent.dev) | Always (if annotated) | `/a2a/{ns}/{name}` |
| `AgentgatewayBackend` with `spec.ai` | AI backend | `/llm/{ns}/{name}` |
| `AgentgatewayBackend` with `spec.mcp` | MCP backend | `/mcp/{ns}/{name}` |

The `{ns}` segment defaults to the resource's namespace, but `platform.agentic.io/expose-alias` overrides it (e.g., `default`). Generated routes include `ownerReferences` so they're garbage-collected with the source resource, and `synchronize: true` keeps them in sync.

### Mutation Policies

| Policy | Trigger | Mutation |
|--------|---------|----------|
| **`kyverno-platform-scheduling.yaml`** | Pods in namespaces with `platform.agentic.io/component` label, no existing `nodeSelector` | Injects `nodeSelector: {node-role: platform}` + toleration for `workload=platform` |
| **`kyverno-tenant-scheduling.yaml`** | Pods in namespaces with `platform.agentic.io/tenant: "true"` label | Injects `nodeSelector: {node-role: agents}` + toleration for `workload=agents` |

## Environments

Helmfile supports multiple environments. Values are layered: `defaults.yaml` is always loaded, then the environment-specific file overrides.

| Environment | File | Overrides |
|-------------|------|-----------|
| `default` | `defaults.yaml` | Cluster name, region, Langfuse endpoint |
| `dev` | `dev.yaml` | S3 bucket name (`agentic-platform-langfuse-dev`) |

Deploy a specific environment:

```bash
helmfile -e dev sync
```

## Secrets

The platform expects these secrets to be pre-created by `scripts/02-create-secrets.sh`:

| Secret | Namespace | Contents |
|--------|-----------|----------|
| `langfuse-db-credentials` | langfuse | PostgreSQL DATABASE_URL |
| `langfuse-redis-credentials` | langfuse | Redis host + auth token |
| `langfuse-clickhouse-credentials` | langfuse | ClickHouse user + password |
| `langfuse-auth-secrets` | langfuse | NEXTAUTH_SECRET, SALT, ENCRYPTION_KEY |
| `langfuse-api-keys` | langfuse | Platform Langfuse public/secret keys |
| `otel-collector-auth` | langfuse | Basic auth header for OTEL export |
| `keycloak-db-credentials` | keycloak | PostgreSQL password |
| `keycloak-admin-credentials` | keycloak | Keycloak admin password |
| `langfuse-otel-auth` | kagent-system | OTEL_HEADERS for kagent traces |
| `langfuse-otel-auth` | agentgateway-system | OTEL basic auth for AgentGateway |
| `anthropic-api-secret` | agentgateway-system | Real Anthropic API key (post-install substitution) |
| `kagent-anthropic` | kagent-system | Dummy key for LiteLLM validation |
| `grafana-mcp-token` | kagent-system | Grafana API token (created by post-install script) |
| `evermemos-secrets` | evermemos | MongoDB credentials, OpenRouter API key, DeepInfra API key (embedding + reranking) |

## Resource Budgets

Representative resource requests/limits for key components:

| Component | Requests | Limits | Node Group |
|-----------|----------|--------|------------|
| istiod | 100m / 256Mi | 500m / 1Gi | platform |
| ztunnel | (DaemonSet) | — | all nodes |
| AgentGateway (ingress) | 200m / 256Mi | 1 / 512Mi | gateway |
| AgentGateway (waypoint) | 100m / 128Mi | 500m / 256Mi | agents |
| kagent controller | (see values) | — | platform |
| Langfuse web | 100m / 128Mi | 500m / 512Mi | platform |
| ClickHouse | 200m / 512Mi | 1 / 2Gi | platform |
| ClickHouse Keeper | 50m / 128Mi | 250m / 512Mi | platform |
| Prometheus | 100m / 256Mi | 500m / 512Mi | platform |
| OTEL Collector | 50m / 64Mi | 200m / 256Mi | platform |
| Sandbox Router | 50m / 64Mi | 250m / 256Mi | platform |
| Kyverno (admission) | 100m / 128Mi | 100m / 384Mi | platform |
| EverMemOS app | 100m / 256Mi | 1 / 1Gi | platform |
| EverMemOS MongoDB | 100m / 256Mi | 1 / 2Gi | platform |
| EverMemOS Elasticsearch | 200m / 1Gi | 2 / 2Gi | platform |
| EverMemOS Milvus | 250m / 1Gi | 2 / 4Gi | platform |
| EverMemOS etcd | 100m / 256Mi | 500m / 512Mi | platform |
| EverMemOS MinIO | 100m / 256Mi | 500m / 1Gi | platform |
| EverMemOS Redis | 100m / 128Mi | 250m / 256Mi | platform |
