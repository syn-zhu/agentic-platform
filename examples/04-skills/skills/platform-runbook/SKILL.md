---
name: skill-platform-runbook
description: Operational runbook for the agentic platform — health checks, troubleshooting, and diagnostics.
---

# Platform Runbook

Operational procedures for diagnosing and troubleshooting the agentic platform.

## Quick Diagnostics

Run the diagnostics script to check the local agent environment:

```bash
python3 scripts/diagnostics.py
```

This reports the agent's namespace, service account, loaded skills, and environment configuration.

## Health Check

Check pod health across all platform namespaces using the `k8s_get_resources` tool.

For each of these namespaces, call `k8s_get_resources` with `resource_type: "pods"` and the namespace:

- `kagent-system`
- `agentgateway-system`
- `agentregistry`
- `istio-system`

For each pod, report:
- Name, phase (Running/Pending/Failed), ready containers vs total, restart count
- Flag any pod that is not Running or has restarts > 3 as a WARNING
- Summarize overall health at the end

## List Platform Resources

Use `k8s_get_resources` to list resources across the cluster. Common queries:

| What to check | resource_type | Namespace |
|---|---|---|
| Agents | `agents.kagent.dev` | (all namespaces) |
| MCP servers | `remotemcpservers.kagent.dev` | `kagent-system` |
| Model configs | `modelconfigs.kagent.dev` | (all namespaces) |
| Gateways | `gateways.gateway.networking.k8s.io` | (all namespaces) |
| HTTP routes | `httproutes.gateway.networking.k8s.io` | (all namespaces) |

## Platform Components

The agentic platform consists of these core namespaces:

| Namespace | Component | Purpose |
|-----------|-----------|---------|
| kagent-system | Controller + Agents | Agent lifecycle, built-in agents (k8s, helm, observability) |
| agentgateway-system | Gateway Proxy + Waypoints | LLM routing, A2A proxy, prompt guards |
| agentregistry | Registry | MCP server/agent/skill catalog + deployment |
| istio-system | Service Mesh | Ambient mesh, mTLS, L4/L7 traffic management |
| langfuse | Tracing | OpenTelemetry trace collection and visualization |

## Troubleshooting Guide

All steps below use tools available to this agent — `k8s_get_resources`,
`k8s_get_pod_logs`, `k8s_describe_resource`, and `k8s_get_events`.

### Agent pod in CrashLoopBackOff
1. Use `k8s_get_resources` with `resource_type: "modelconfigs.kagent.dev"` in the agent's namespace — verify a ModelConfig exists
2. Use `k8s_get_resources` with `resource_type: "secrets"` in the namespace — verify the API key secret is present
3. Use `k8s_describe_resource` on the pod — check for ImagePullBackOff in container statuses
4. Use `k8s_get_pod_logs` on the pod — look for startup errors

### LLM requests failing (503 / TLS errors)
1. Use `k8s_get_resources` with `resource_type: "pods"` in `agentgateway-system` — verify the proxy pod is Running
2. Use `k8s_describe_resource` on the `agentgateway-system` namespace — check that `istio.io/dataplane-mode` label is NOT set (it should not be in the mesh)
3. Use `k8s_get_resources` with `resource_type: "gateways.gateway.networking.k8s.io"` — verify the per-tenant waypoint is Programmed
4. Use `k8s_get_resources` with `resource_type: "agentgatewaybackends.agentgateway.dev"` — verify Accepted status
5. Use `k8s_get_events` in `agentgateway-system` — look for errors related to LLM routing

### Agent not reachable via A2A
1. Use `k8s_describe_resource` on the agent — verify it has annotation `platform.agentic.io/expose: "true"`
2. Use `k8s_get_resources` with `resource_type: "httproutes.gateway.networking.k8s.io"` in the agent's namespace — verify Kyverno generated an HTTPRoute
3. Use `k8s_describe_resource` on the namespace — verify label `istio.io/ingress-use-waypoint: "true"` is set
4. Use `k8s_get_events` in the agent's namespace — look for routing or connectivity errors

### Skills not loading
1. Use `k8s_describe_resource` on the agent pod — check the `skills-init` init container status for image pull errors
2. Use `k8s_get_pod_logs` on the pod with `container: "skills-init"` — check for extraction errors
3. Run `ls /skills/` via `bash` — verify the skill directory exists and contains SKILL.md

### MCP tools not connecting
1. Use `k8s_get_resources` with `resource_type: "remotemcpservers.kagent.dev"` in `kagent-system` — verify the RemoteMCPServer CR exists
2. Use `k8s_get_resources` with `resource_type: "pods"` in `kagent-system` — verify the MCP server pod is Running
3. Use `k8s_get_events` in `kagent-system` — look for connectivity or timeout errors
