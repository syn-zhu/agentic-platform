# Agentic Platform

A production-grade, multi-tenant Kubernetes platform for deploying, managing, and securing AI agents at scale. Built on AWS EKS with Istio Ambient Mesh, it provides enterprise-level isolation, observability, and governance for agentic workloads.

## Architecture Overview

The platform combines two open-source projects — [AgentGateway](https://github.com/agentgateway/agentgateway) (data plane) and [kagent](https://github.com/kagent-dev/kagent) (control plane) — with Istio Ambient Mesh, Keycloak, and a suite of Kyverno policies to form a complete multi-tenant agent runtime.

![Architecture](architecture.drawio.svg)

### Key Design Decisions

- **Istio Ambient Mesh** — Zero-sidecar service mesh. L4 mTLS via ztunnel on every node; L7 policy via per-tenant waypoint proxies. No sidecar injection overhead.
- **AgentGateway as Waypoint** — The waypoint proxy implementation is AgentGateway (not Envoy), giving waypoints native understanding of MCP, A2A, and LLM protocols.
- **Credential Injection at Gateway** — Tenants never hold real LLM API keys. Dummy secrets pass LiteLLM validation; the gateway strips and injects real credentials at the proxy layer.
- **Kyverno-Driven Conventions** — Policies auto-generate HTTPRoutes from annotations, inject node scheduling, and enforce trace propagation. Tenants opt in with `platform.agentic.io/expose: "true"`.

## Components

| Component | Role | Namespace |
|-----------|------|-----------|
| **AgentGateway** | L7 proxy — routes MCP, A2A, and LLM traffic; enforces auth policies; injects credentials; traces to Langfuse | `agentgateway-system` |
| **kagent** | K8s-native agent controller — reconciles Agent, ModelConfig, MCPServer CRDs into running workloads | `kagent-system` |
| **Istio (Ambient)** | mTLS, SPIFFE identity, ztunnel (L4), waypoint enrollment | `istio-system` |
| **Keycloak** | Identity provider — JWT issuance, tenant claims | `keycloak` |
| **Langfuse** | LLM observability — OTEL trace ingestion, prompt/completion logging, cost tracking | `langfuse` |
| **ClickHouse** | Analytics database backing Langfuse | `langfuse` |
| **Prometheus + Grafana** | Metrics collection, dashboards, alerting | `monitoring` |
| **Kyverno** | Policy engine — 5 mutation/generation policies for scheduling, routing, and protocol detection | `kyverno` |
| **OTEL Collector** | Bridges agent gRPC OTLP to Langfuse HTTP OTLP endpoint | `langfuse` |

## Node Groups

The EKS cluster uses three tainted node groups to isolate workload types:

| Node Group | Instance | Taint | Workloads |
|------------|----------|-------|-----------|
| **platform** | t3.large (1-3) | `workload=platform:NoSchedule` | Controllers, Langfuse, Prometheus, Keycloak, Kyverno |
| **agents** | t3.large (1-3) | `workload=agents:NoSchedule` | Tenant agent pods, MCP servers, waypoint proxies |
| **gateway** | t3.medium (1-2) | `workload=gateway:NoSchedule` | AgentGateway ingress proxy (NLB-backed) |

Kyverno policies automatically inject the correct `nodeSelector` and `tolerations` — platform operators and tenants don't need to specify them.

## Multi-Tenancy

Tenant isolation is enforced in depth across five layers:

1. **Namespace** — Each tenant gets a dedicated namespace with resource quotas and limit ranges
2. **RBAC** — `tenant-agent-developer` ClusterRole scoped per-namespace via RoleBinding
3. **NetworkPolicy** — Deny-by-default at the CNI layer; explicit allow for DNS, OTEL, gateway, and external HTTPS
4. **Istio AuthorizationPolicy** — L4 isolation via SPIFFE identity through ztunnel (independent of NetworkPolicy)
5. **Per-Tenant Waypoint** — Each namespace gets its own AgentGateway waypoint for L7 policy (prompt guards, credential injection)

Tenants can:
- Deploy Agents, MCPServers, and Sandboxes in their namespace
- Bring their own LLM keys (BYOK pattern via AgentgatewayBackend)
- Expose agents via annotation — Kyverno auto-generates HTTPRoutes
- Call agents in other namespaces through the central gateway

## Directory Structure

```
.
├── cluster/          # EKS cluster definition (eksctl) and IAM policies
├── platform/         # Helmfile, Helm values, Kubernetes manifests, and custom images
│   ├── helmfile.yaml       # Orchestrates ~12 Helm releases in phased order
│   ├── environments/       # Per-environment config (dev, defaults)
│   ├── images/             # Custom container images
│   ├── manifests/          # Post-install K8s manifests (policies, routes, RBAC)
│   └── values/             # Helm chart value overrides
├── scripts/          # Numbered deployment pipeline (00-05) and utilities
├── tenants/          # Tenant templates, examples, and onboarded tenant configs
│   ├── _template/          # Boilerplate YAML for tenant onboarding
│   ├── examples/           # Sample agent, MCP server, sandbox, and backend configs
│   └── onboarded/          # Live tenant directories (alpha, beta, test-a2a)
├── tests/            # Kyverno unit tests and Chainsaw e2e integration tests
│   ├── kyverno/            # 5 policy test suites
│   └── e2e/                # 2 Chainsaw test suites (passthrough, egress)
└── vendor/           # Patched forks of agentgateway and kagent
```

See individual directory READMEs for detailed documentation.

## Getting Started

### Prerequisites

- AWS CLI configured with appropriate permissions
- `eksctl`, `kubectl`, `helm`, `helmfile` installed
- `kyverno` CLI (for running policy tests)
- `chainsaw` (for e2e tests)
- An Anthropic API key

### Deployment Pipeline

The scripts in `scripts/` are numbered in execution order:

```bash
# 1. Create the EKS cluster (15-20 min)
./scripts/00-create-cluster.sh

# 2. Provision AWS resources (RDS PostgreSQL, ElastiCache Redis, S3)
./scripts/01-create-aws-resources.sh

# 3. Create Kubernetes secrets from AWS outputs
./scripts/02-create-secrets.sh

# 4. Deploy all platform services via Helmfile
./scripts/03-deploy-platform.sh

# 5. Apply post-install manifests (Kyverno policies, Grafana MCP, Agent Sandbox CRDs)
./scripts/04-post-install.sh

# 6. Onboard a tenant
./scripts/05-onboard-tenant.sh alpha
```

### Accessing Platform UIs

```bash
./scripts/port-forward.sh
```

| Service | URL |
|---------|-----|
| kagent UI | http://localhost:15000 |
| Langfuse | http://localhost:15001 |
| Grafana | http://localhost:15002 |
| AgentGateway | http://localhost:15003 |
| Keycloak | http://localhost:15004 |
| Kiali | http://localhost:15005 |

### Quick Reapply

After editing manifests, reapply without a full redeploy:

```bash
./scripts/apply.sh              # Full reapply (Helmfile + manifests)
./scripts/apply.sh --skip-helm  # Manifests only
```

## Routing

All agent, LLM, and tool traffic flows through the AgentGateway proxy:

| Path Pattern | Target | Protocol |
|--------------|--------|----------|
| `/a2a/{namespace}/{agent-name}` | Agent pods | A2A (Google Agent-to-Agent) |
| `/llm/{namespace}/{model}` | LLM backends (Anthropic, OpenAI, etc.) | HTTP (with credential injection) |
| `/mcp/{namespace}/{server}` | MCP tool servers | MCP (Model Context Protocol) |
| `/sandbox/*` | Dynamic sandbox router | HTTP (header-based routing) |

Routes are auto-generated by Kyverno when resources are annotated with `platform.agentic.io/expose: "true"`. The optional `platform.agentic.io/expose-alias` annotation overrides the namespace segment in the path.

## Observability

Agent interactions are traced end-to-end (shown in the architecture diagram above). Langfuse captures prompt/completion pairs, token usage, latency, and cost. Traces follow W3C `traceparent` headers across agent-to-agent calls (Kyverno injects `DISABLE_AIOHTTP_TRANSPORT=True` to ensure `httpx` propagates context correctly).

Prometheus scrapes metrics from all namespaces. Grafana dashboards are auto-discovered, and a Grafana MCP tool server is available to kagent's built-in observability agent.

## Testing

```bash
# Run all tests
./tests/run-all.sh

# Unit tests only (Kyverno policy validation, no cluster required)
./tests/run-all.sh --unit-only

# Integration tests only (requires running cluster)
./tests/run-all.sh --integration-only
```

### Kyverno Policy Tests (Unit)
- **platform-scheduling** — Injects platform node affinity
- **tenant-scheduling** — Injects agent node affinity
- **auto-expose** — Generates HTTPRoutes from annotations
- **traceprop** — Injects trace propagation env vars
- **appprotocol** — Marks A2A services with `kgateway.dev/a2a` appProtocol

### Chainsaw E2E Tests (Integration)
- **waypoint-passthrough** — Verifies intra-namespace traffic routes through waypoint
- **waypoint-egress** — Tests external LLM API calls with TLS origination and credential injection

## Vendored Dependencies

The `vendor/` directory contains patched forks of the two core projects:

- **agentgateway** (Rust) — Patched for Istio Ambient waypoint integration and HBONE protocol support. Branch: `waypoint`.
- **kagent** (Go/Python/TypeScript) — Patched for trace context propagation (traceparent/tracestate) and A2A discovery for agent Services. Branch: `fix/agent-service-a2a-appprotocol` (PR #1297).

Both are upstream-tracking forks. Changes are scoped to features not yet merged upstream.

## External Dependencies (AWS)

| Service | Purpose | Configuration |
|---------|---------|---------------|
| RDS PostgreSQL 17 | Keycloak + Langfuse database | `db.t4g.small`, encrypted, 20GB gp3 |
| ElastiCache Redis | Langfuse session/cache | `cache.t4g.small`, transit encryption |
| S3 | Langfuse event/media uploads | `agentic-platform-langfuse-{env}`, AES256, no public access |
| EBS (gp3) | Persistent volumes for ClickHouse, Prometheus, Grafana | Provisioned via EBS CSI driver |
