"""BYO ADK agent that discovers, deploys, and uses MCP servers on-demand.

The agent connects to AgentRegistry via a static MCP toolset for catalog
search and deployment.  After deploying a server it reaches it through
custom Python tools that open ad-hoc MCP sessions to the new Service.
"""

from __future__ import annotations

import os

from google.adk.agents import Agent
from google.adk.models.lite_llm import LiteLlm
from google.adk.tools.mcp_tool import StreamableHTTPConnectionParams
from google.adk.tools.mcp_tool.mcp_toolset import McpToolset

from .tools import call_mcp_tool, list_server_tools

# ---------------------------------------------------------------------------
# LLM — Claude via AgentGateway proxy
# ANTHROPIC_API_KEY env is a dummy; real key is injected at the gateway layer.
# ---------------------------------------------------------------------------
model = LiteLlm(
    model=os.environ.get("LLM_MODEL", "anthropic/claude-sonnet-4-20250514"),
    base_url=os.environ.get(
        "LLM_BASE_URL",
        "http://agentgateway-proxy.agentgateway-system.svc.cluster.local"
        "/llm/default/anthropic",
    ),
)

# ---------------------------------------------------------------------------
# Static MCP connection — AgentRegistry (catalog search + deploy)
# ---------------------------------------------------------------------------
registry_mcp = McpToolset(
    connection_params=StreamableHTTPConnectionParams(
        url=os.environ.get(
            "REGISTRY_MCP_URL",
            "http://agentregistry.agentregistry.svc.cluster.local:8090/mcp",
        ),
    ),
    tool_filter=[
        "list_servers",
        "get_server",
        "get_server_readme",
        "deploy_server",
    ],
)

# ---------------------------------------------------------------------------
# Root agent
# ---------------------------------------------------------------------------
root_agent = Agent(
    model=model,
    name="mcp_deployer",
    description="Discovers, deploys, and uses MCP servers on-demand",
    instruction="""\
You are a platform assistant that can dynamically extend your own capabilities
by finding MCP tool servers in a registry and using their tools at runtime.

The registry contains two kinds of servers:
- **Remote** servers — already running at a public URL. Use them directly.
- **Stdio** servers — packaged as npm/pypi/OCI. Must be deployed to Kubernetes
  first via `deploy_server`.

## Workflow

When a user asks you to do something and you don't already have the right tool:

1. **Search** the catalog:
   Call `list_servers` with a relevant search query.
   Set `semantic=true` for natural-language queries.

2. **Inspect** the result:
   Call `get_server` to see the full server details.
   - If the server has a `remotes` array with a URL → it is a **remote** server.
   - If it only has `packages` → it is a **stdio** server that needs deployment.

3a. **Remote servers** (has `remotes` URL):
   - Skip deployment — the server is already running.
   - Call `list_server_tools(url="<the remote URL>")` to discover tools.
   - Call `call_mcp_tool(tool_name=..., arguments=..., url="<the remote URL>")`
     to call a tool.

3b. **Stdio servers** (needs deployment):
   - Optionally read the README: `get_server_readme` to check required config.
   - Deploy: `deploy_server` with the server name, version ("latest" if unsure),
     and **always set `runtime` to `"kubernetes"`**.
     Pass `config` if the README says API keys or settings are needed.
   - List tools: `list_server_tools(server_name="<exact name from deploy_server>")`.
     The server needs 30-60 s to start — the function retries automatically.
   - Call a tool: `call_mcp_tool(tool_name=..., arguments=..., server_name="<name>")`.

## Important notes

- **Always pass `runtime: "kubernetes"` to `deploy_server`.**  The default is
  "local" (Docker Compose), which does not work in this environment.
- Prefer remote servers when available — they're instantly usable, no deploy needed.
- For `list_server_tools` / `call_mcp_tool`, pass EITHER `url` (remote) or
  `server_name` (deployed), never both.
- After `deploy_server`, allow the pod time to start. If `list_server_tools`
  returns an error, wait and retry.
- Some servers require configuration (API keys, tokens). Always check the
  server details or README first.
""",
    tools=[registry_mcp, list_server_tools, call_mcp_tool],
)
