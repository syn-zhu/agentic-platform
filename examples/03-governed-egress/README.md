# Example 03: Governed AI Egress

Demonstrates **per-tenant egress governance** using the AgentGateway waypoint proxy. The agent thinks it's talking directly to `api.anthropic.com`, but the mesh transparently intercepts the traffic and routes it through a per-tenant waypoint that:

1. **Applies prompt guards** — regex-based PII filtering blocks SSNs and credit card numbers before they reach the LLM
2. **Injects credentials** — the agent only has a dummy API key; the waypoint adds the real one
3. **Originates TLS** — HTTP inside the mesh becomes HTTPS to the external API

## Architecture

![Architecture](architecture.drawio.svg)

### How it differs from Examples 01 & 02

| | Examples 01/02 | Example 03 |
|---|---|---|
| LLM routing | Shared ingress proxy in `agentgateway-system` | Per-tenant waypoint in tenant namespace |
| ModelConfig base URL | `http://agentgateway-proxy.agentgateway-system.svc.cluster.local/...` | `http://api.anthropic.com` (mesh-intercepted) |
| Credential location | Centralized in `agentgateway-system` | Isolated in tenant namespace |
| Prompt guards | None | Per-tenant PII filtering |

## Prerequisites

- Platform deployed (`scripts/00` through `04`)
- `ANTHROPIC_API_KEY` environment variable set

## Deploy

```bash
# 1. Create the real API key secret (waypoint needs it for credential injection)
kubectl create secret generic anthropic-api-secret \
  -n example-egress \
  --from-literal=Authorization="$ANTHROPIC_API_KEY"

# 2. Deploy the example
kubectl apply -f examples/03-governed-egress/manifests.yaml
```

The secret is created separately because it contains a real credential — demonstrating that credentials live in the tenant namespace, not in a shared system namespace.

## Try It

### Via kagent UI

Open the kagent UI, select **governed-assistant**, and try:

1. **Normal query** (should work):
   > What is the capital of France?

2. **SSN in prompt** (should be blocked):
   > Is 123-45-6789 a valid SSN?

3. **Credit card in prompt** (should be blocked):
   > Can you verify this credit card: 4111-1111-1111-1111?

PII queries are blocked at the waypoint *before* reaching the LLM — the agent sees a 403 error with "Request blocked by tenant policy: PII detected in prompt".

### Via curl (raw HTTP through waypoint)

From a pod inside the namespace:

```bash
# Normal query — 200
curl -s -X POST http://api.anthropic.com/v1/messages \
  -H 'Content-Type: application/json' \
  -H 'x-api-key: not-a-real-key' \
  -H 'anthropic-version: 2023-06-01' \
  -d '{"model":"claude-sonnet-4-20250514","max_tokens":50,"messages":[{"role":"user","content":"Hello"}]}'

# SSN in prompt — 403
curl -s -X POST http://api.anthropic.com/v1/messages \
  -H 'Content-Type: application/json' \
  -H 'x-api-key: not-a-real-key' \
  -H 'anthropic-version: 2023-06-01' \
  -d '{"model":"claude-sonnet-4-20250514","max_tokens":50,"messages":[{"role":"user","content":"Is 123-45-6789 a valid SSN?"}]}'
# → "Request blocked by tenant policy: PII detected in prompt"
```

## Verify

Check the waypoint logs to see prompt guard rejections:

```bash
kubectl logs -n example-egress \
  -l gateway.networking.k8s.io/gateway-name=agentgateway-waypoint \
  --tail=10
```

You should see:
- Normal queries: `http.status: 200`, `protocol: llm`, `gen_ai.usage.input_tokens: ...`
- Blocked queries: `http.status: 403`, `reason: DirectResponse`, `duration: 0ms`

## Key Resources

| Resource | Purpose |
|----------|---------|
| `ServiceEntry/anthropic-api` | Tells the mesh to intercept `api.anthropic.com` traffic |
| `AgentgatewayBackend/anthropic-egress` | Prompt guards + credential injection + route policies |
| `HTTPRoute/anthropic-egress` | Routes intercepted traffic through the backend |
| `Secret/anthropic-api-secret` | Real API key (created manually, not in manifests) |
| `Secret/anthropic-dummy` | Dummy key for LiteLLM validation |
| `Gateway/agentgateway-waypoint` | Per-tenant waypoint proxy |

## Cleanup

```bash
kubectl delete -f examples/03-governed-egress/manifests.yaml
kubectl delete secret anthropic-api-secret -n example-egress  # if namespace deletion hangs
```
