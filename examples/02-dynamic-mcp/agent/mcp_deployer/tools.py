"""Custom tools for dynamically connecting to MCP servers.

Supports two modes:
  1. **Remote** — server already running at a public URL (from the registry's
     ``remotes`` field).  Pass the URL directly.
  2. **Deployed** — server deployed to Kubernetes via ``deploy_server``.  Pass
     the server name and the URL is derived from the in-cluster Service.
"""

from __future__ import annotations

import asyncio
import json
import os
import re

from mcp import ClientSession
from mcp.client.streamable_http import streamablehttp_client

DEFAULT_NAMESPACE = os.environ.get("MCP_SERVER_NAMESPACE", "agentregistry")
DEFAULT_PORT = int(os.environ.get("MCP_SERVER_PORT", "3000"))


def _sanitize_k8s_name(name: str) -> str:
    """Mirror AgentRegistry's sanitizeK8sName (Go) in Python.

    Rules: lowercase → replace non-[a-z0-9] with '-' → collapse runs of '-'
    → trim leading/trailing '-' → truncate to 63 chars.
    """
    name = name.lower()
    name = re.sub(r"[^a-z0-9-]", "-", name)
    name = re.sub(r"-+", "-", name)
    return name.strip("-")[:63]


def _deployed_server_url(server_name: str) -> str:
    sanitized = _sanitize_k8s_name(server_name)
    return f"http://{sanitized}.{DEFAULT_NAMESPACE}.svc.cluster.local:{DEFAULT_PORT}/mcp"


def _resolve_url(server_name: str | None, url: str | None) -> str:
    """Return the MCP endpoint URL from either an explicit URL or a server name."""
    if url:
        return url
    if server_name:
        return _deployed_server_url(server_name)
    raise ValueError("Provide either server_name or url")


async def list_server_tools(
    server_name: str | None = None,
    url: str | None = None,
) -> str:
    """List available tools on an MCP server.

    For **remote** servers (already running), pass ``url`` — the remote
    endpoint from the registry (e.g. "https://mcp.exa.ai/mcp").

    For **deployed** servers (created via deploy_server), pass
    ``server_name`` — the exact name used in deploy_server.  The in-cluster
    Service URL is derived automatically.  Retries for up to ~50 s while the
    pod starts.

    Args:
        server_name: Server name from deploy_server (for deployed servers).
        url: Direct MCP endpoint URL (for remote servers).
    """
    resolved = _resolve_url(server_name, url)
    is_deployed = url is None  # deployed servers may need startup time
    max_attempts = 5 if is_deployed else 2
    last_error: str = ""

    for attempt in range(max_attempts):
        try:
            async with streamablehttp_client(resolved) as (read, write, _):
                async with ClientSession(read, write) as session:
                    await session.initialize()
                    result = await session.list_tools()
                    tools = [
                        {
                            "name": t.name,
                            "description": t.description,
                            "inputSchema": t.inputSchema,
                        }
                        for t in result.tools
                    ]
                    return json.dumps(
                        {
                            "server": server_name or url,
                            "url": resolved,
                            "tools": tools,
                        },
                        indent=2,
                    )
        except Exception as exc:
            last_error = str(exc)
            if attempt < max_attempts - 1:
                await asyncio.sleep(10 if is_deployed else 3)

    return json.dumps(
        {
            "error": last_error,
            "url": resolved,
            "hint": "Server may still be starting. Try again in 30 s."
            if is_deployed
            else "Remote server may be unreachable.",
        }
    )


async def call_mcp_tool(
    tool_name: str,
    arguments: dict,
    server_name: str | None = None,
    url: str | None = None,
) -> str:
    """Call a specific tool on an MCP server.

    For **remote** servers, pass ``url``.
    For **deployed** servers, pass ``server_name``.

    Args:
        tool_name:   The tool name returned by list_server_tools.
        arguments:   A JSON object matching the tool's input schema.
        server_name: Server name from deploy_server (for deployed servers).
        url: Direct MCP endpoint URL (for remote servers).
    """
    resolved = _resolve_url(server_name, url)
    try:
        async with streamablehttp_client(resolved) as (read, write, _):
            async with ClientSession(read, write) as session:
                await session.initialize()
                result = await session.call_tool(tool_name, arguments)
                contents = [c.model_dump() for c in result.content]
                return json.dumps(
                    {"tool": tool_name, "result": contents}, indent=2
                )
    except Exception as exc:
        return json.dumps({"error": str(exc), "url": resolved})
