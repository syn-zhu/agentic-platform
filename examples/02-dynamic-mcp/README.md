# Example 02: Dynamic MCP

A "self-extending" agent that discovers MCP servers in the AgentRegistry catalog, connects to remote servers directly or deploys stdio servers on-demand to Kubernetes, and calls their tools dynamically at runtime — no pre-configured tool bindings required.

## What It Demonstrates

- **BYO (Bring Your Own) agent** — a custom ADK agent packaged as a Docker image, deployed via kagent's `type: BYO` Agent CR (vs. the declarative agents in Example 01)
- **Dynamic tool discovery** — searches the AgentRegistry MCP server catalog with semantic search to find relevant tool servers
- **Dual-mode connectivity** — automatically determines how to reach a server:
  - **Remote servers** (already running at a URL) — connects directly, no deployment needed
  - **Stdio servers** (npm/pypi/OCI packages) — deploys to Kubernetes via `deploy_server`, then connects to the in-cluster Service
- **Runtime MCP sessions** — opens ad-hoc MCP client sessions using custom Python tools
- **Self-extending capabilities** — the agent starts with no domain-specific tools but can use any server in the catalog on the fly

## Architecture

```
                          ┌────────────────────────────┐
                          │   AgentRegistry (8090)      │
                          │   MCP Server                │
                          │   • list_servers (semantic)  │
                          │   • get_server / get_readme  │
                          │   • deploy_server            │
                          └──────────▲─────────────────┘
                                     │ Static MCP connection
                                     │
┌──────────┐  A2A   ┌───────────────┤
│  curl /  │ ─────► │  mcp-agent    │   Custom Python tools
│  kagent  │        │  (BYO ADK)    ├──────────────────────────┐
│  UI      │        │               │  list_server_tools()     │
└──────────┘        └──────┬────────┘  call_mcp_tool()         │
                    example-mcp    │                            │
                                   │                            │
                    ┌──────────────┴──────────────┐             │
                    ▼                              ▼             │
        ┌───────────────────┐         ┌──────────────────┐      │
        │ Remote MCP server │◄────────│ Deployed MCP     │◄─────┘
        │ (public URL)      │  direct │ server (Pod+Svc) │ in-cluster
        │ e.g. mcp.exa.ai  │         │ default:3000     │
        └───────────────────┘         └──────────────────┘
```

**Remote server flow** (e.g. "search the web for Kubernetes news"):
1. Agent searches the catalog → finds `ai.exa/exa` (remote, URL: `https://mcp.exa.ai/mcp`)
2. Agent calls `list_server_tools(url="https://mcp.exa.ai/mcp")` → discovers tools
3. Agent calls `call_mcp_tool(url="https://mcp.exa.ai/mcp", tool_name="web_search", arguments={...})`
4. Result is returned to the user

**Stdio server flow** (e.g. "what's the weather in San Francisco?"):
1. Agent searches the catalog → finds `io.github.dgahagan/weather-mcp` (stdio, npm package)
2. Agent deploys: `deploy_server(serverName="io.github.dgahagan/weather-mcp", version="latest")`
3. KMCP creates a Deployment + Service in the `default` namespace
4. Agent calls `list_server_tools(server_name="io.github.dgahagan/weather-mcp")`
5. Agent calls `call_mcp_tool(server_name="...", tool_name="get_forecast", arguments={...})`

## How It Differs from Example 01

| | Example 01 | Example 02 |
|--|-----------|-----------|
| **Agent type** | Declarative (YAML-defined) | BYO (custom Python code) |
| **Tools** | Static MCP bindings | Dynamic discovery + connection |
| **Model config** | ModelConfig CR | LiteLlm in Python code |
| **Capabilities** | Fixed at deploy time | Self-extending at runtime |
| **Framework** | kagent declarative spec | Google ADK (what kagent is built on) |

## Prerequisites

1. Platform deployed (`platform/manifests/`)
2. AgentRegistry seeded with MCP servers (builtin seed enabled in the AgentRegistry deployment)
3. AgentRegistry running v0.4.3+ (includes the stdio port fix for `deploy_server`)

## Deploy

```bash
kubectl apply -f manifests.yaml
```

Wait for the agent to be ready:

```bash
kubectl get agents -n example-mcp
kubectl get pods -n example-mcp -w
```

## Try It

### Option 1: kagent UI (recommended)

1. Port-forward the kagent UI:
   ```bash
   kubectl port-forward -n kagent-system svc/kagent-ui 15000:8080
   ```

2. Open http://localhost:15000 in your browser

3. Select the **mcp-agent** agent and try:

   > "What MCP servers are available in the catalog?"

   The agent will call `list_servers` and show the registry contents.

   > "Search the web for the latest Kubernetes 1.33 release notes"

   The agent will find a remote web search server (e.g. Exa), connect directly, and call its search tool.

   > "Find me a tool that can work with GitHub repositories"

   The agent will use semantic search to find relevant servers.

### Option 2: A2A via curl

Port-forward the AgentGateway:

```bash
kubectl port-forward -n agentgateway-system svc/agentgateway-proxy 15003:80
```

```bash
curl -s --max-time 180 -X POST http://localhost:15003/a2a/example-mcp/mcp-agent \
  -H 'Content-Type: application/json' \
  -d '{
    "jsonrpc": "2.0",
    "method": "message/send",
    "id": "1",
    "params": {
      "message": {
        "role": "user",
        "messageId": "msg-001",
        "parts": [{"kind": "text", "text": "What MCP servers are available in the catalog?"}]
      }
    }
  }'
```

> **Note:** Requests involving stdio deployment + tool calls can take 60-90
> seconds (pod startup). Remote servers are nearly instant. The kagent UI
> handles streaming responses better than blocking curl.

## Verify

Check that the agent is running:

```bash
kubectl get pods -n example-mcp
kubectl logs -n example-mcp deployment/mcp-agent --tail=20
```

After the agent deploys a stdio server, verify the MCPServer CR was created:

```bash
kubectl get mcpservers -n default
kubectl get pods -n default -l app.kubernetes.io/managed-by=kmcp
```

Check the auto-generated HTTPRoute (created by Kyverno):

```bash
kubectl get httproute -n example-mcp
```

## Building the Docker Image

If you need to rebuild the agent image:

```bash
cd agent
docker buildx build --platform linux/amd64 \
  -t <your-registry>/mcp-deployer:v0.2.0 \
  -f Dockerfile .
docker push <your-registry>/mcp-deployer:v0.2.0
```

Then update the image reference in `manifests.yaml`.

## Cleanup

```bash
# Remove the agent and namespace
kubectl delete -f manifests.yaml

# Also clean up any dynamically deployed MCP servers
kubectl delete mcpservers --all -n default
```
