# Example 01: Agent Discovery & Invocation

A platform assistant agent that discovers deployed agents at runtime and dynamically invokes them via A2A — no static wiring or manual registration required.

## What It Demonstrates

- **Runtime agent discovery** — query the AgentRegistry deployment tracker (`list_deployments`) to see every agent and MCP server currently running on the platform
- **Dynamic A2A invocation** — call any discovered agent via kagent's `invoke_agent` tool, using east-west kube DNS (no ingress gateway needed)
- **Kyverno auto-expose** — `platform.agentic.io/expose: "true"` auto-generates an HTTPRoute for north-south access
- **Credential injection** — the agent uses a dummy Anthropic key; the real key is injected by AgentGateway at the proxy layer

## Architecture

```
                        ┌──────────────────────────┐
                        │   AgentRegistry (8090)    │
                        │   Deployment Tracker      │
                        │   (K8s watch → catalog)   │
                        └────────────▲─────────────┘
                                     │ MCP: list_deployments
                                     │
┌──────────┐  A2A   ┌───────────────┐│  invoke_agent   ┌────────────────┐
│  curl /  │ ─────► │  platform-    ├┘ ──────────────► │  k8s-agent     │
│  kagent  │        │  assistant    │  ──────────────► │  helm-agent    │
│  UI      │        │               │       A2A        │  observability │
└──────────┘        └───────────────┘  (kube DNS)      │  ...           │
                      example-discovery                └────────────────┘
                                                         kagent-system
```

**Flow:**
1. User asks the platform-assistant a question (e.g. "check the health of my cluster")
2. The assistant calls `list_deployments` to discover all deployed agents and servers
3. It picks the best agent for the task (e.g. `kagent-system/k8s-agent`)
4. It calls `invoke_agent` with the agent reference and the task
5. The kagent controller sends an A2A message over east-west kube DNS to the target agent
6. The response is returned to the user

## Prerequisites

1. Platform deployed (`platform/manifests/`)
2. kagent-agents RemoteMCPServer applied:
   ```bash
   kubectl apply -f ../../platform/manifests/kagent-agents-remotemcpserver.yaml
   ```

## Deploy

```bash
kubectl apply -f manifests.yaml
```

Wait for the agent to be ready:

```bash
kubectl get agents -n example-discovery
kubectl get pods -n example-discovery -w
```

## Try It

### Option 1: kagent UI (recommended for demos)

1. Port-forward the kagent UI:
   ```bash
   kubectl port-forward -n kagent-system svc/kagent-ui 15000:8080
   ```

2. Open http://localhost:15000 in your browser

3. Select the **platform-assistant** agent and try:

   > "What agents are available on the platform?"

   The assistant will call `list_deployments` and list every deployed agent and server.

   > "Check the health of my cluster"

   The assistant will discover `k8s-agent` and invoke it with the task.

   > "Generate a PromQL query for 95th percentile HTTP latency"

   The assistant will discover `promql-agent` and invoke it.

   > "What Helm releases are running in kagent-system?"

   The assistant will discover `helm-agent` and invoke it.

### Option 2: A2A via curl

Port-forward the AgentGateway:

```bash
kubectl port-forward -n agentgateway-system svc/agentgateway-proxy 15003:80
```

**Discovery query** (fast, single-hop):

```bash
curl -s -X POST http://localhost:15003/a2a/example-discovery/platform-assistant \
  -H 'Content-Type: application/json' \
  -d '{
    "jsonrpc": "2.0",
    "method": "message/send",
    "id": "1",
    "params": {
      "message": {
        "role": "user",
        "messageId": "msg-001",
        "parts": [{"kind": "text", "text": "What agents and servers are deployed on the platform?"}]
      }
    }
  }'
```

**Discovery + invocation** (multi-hop, use kagent UI for best results):

```bash
curl -s --max-time 120 -X POST http://localhost:15003/a2a/example-discovery/platform-assistant \
  -H 'Content-Type: application/json' \
  -d '{
    "jsonrpc": "2.0",
    "method": "message/send",
    "id": "2",
    "params": {
      "message": {
        "role": "user",
        "messageId": "msg-002",
        "parts": [{"kind": "text", "text": "Ask the PromQL agent to generate a query for 95th percentile HTTP latency"}]
      }
    }
  }'
```

> **Note:** Multi-hop requests (discover → invoke → return) take 30-40 seconds.
> The kagent UI (Option 1) handles these async tasks better than blocking curl.

## Verify

Check that the auto-generated HTTPRoute exists (created by Kyverno):

```bash
kubectl get httproute -n example-discovery
```

Check the agent's A2A skill advertisement:

```bash
kubectl port-forward -n agentgateway-system svc/agentgateway-proxy 15003:80
curl -s http://localhost:15003/a2a/example-discovery/platform-assistant/.well-known/agent.json | jq .skills
```

## Cleanup

```bash
kubectl delete -f manifests.yaml
```
