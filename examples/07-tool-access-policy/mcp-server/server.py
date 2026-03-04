"""
Policy MCP Server -- minimal MCP server with role-differentiated tools.

Access control is NOT enforced here. The AgentgatewayPolicy CEL rules at the
waypoint enforce which callers can invoke which tools. This server simply
implements the tool logic.

Tools:
  list_reports   -- list available reports
  read_report    -- read a specific report by ID
  execute_query  -- run an arbitrary query string
  modify_config  -- change a configuration key/value
"""

import os

from mcp.server.fastmcp import FastMCP

mcp = FastMCP("policy-mcp-server")

# In-memory sample data -- enough to demonstrate the tools work.
REPORTS = {
    "rpt-001": {"title": "Q1 Revenue Summary", "status": "final", "content": "Total revenue: $4.2M (+12% YoY)"},
    "rpt-002": {"title": "Monthly Active Users", "status": "draft", "content": "MAU: 84,300 (March 2026)"},
    "rpt-003": {"title": "Infrastructure Cost Breakdown", "status": "final", "content": "Compute: 62%, Storage: 24%, Network: 14%"},
}

CONFIG = {
    "retention_days": "90",
    "max_query_rows": "1000",
    "audit_logging": "true",
}


@mcp.tool()
def list_reports() -> list[dict]:
    """List all available reports with their ID, title, and status."""
    return [
        {"id": rid, "title": r["title"], "status": r["status"]}
        for rid, r in REPORTS.items()
    ]


@mcp.tool()
def read_report(report_id: str) -> dict:
    """Read a specific report by ID. Returns the full report content."""
    report = REPORTS.get(report_id)
    if report is None:
        return {"error": f"Report '{report_id}' not found"}
    return {"id": report_id, **report}


@mcp.tool()
def execute_query(query: str) -> dict:
    """Execute an arbitrary query string. Returns mock results."""
    # This is a demo -- real implementation would run against a database.
    return {
        "query": query,
        "rows_returned": 42,
        "results": [
            {"id": 1, "value": "sample-row-1"},
            {"id": 2, "value": "sample-row-2"},
        ],
        "note": "Mock results -- replace with real query execution",
    }


@mcp.tool()
def modify_config(key: str, value: str) -> dict:
    """Modify a system configuration key. Returns the previous and new values."""
    previous = CONFIG.get(key)
    if previous is None:
        return {"error": f"Unknown config key '{key}'", "valid_keys": list(CONFIG.keys())}
    CONFIG[key] = value
    return {"key": key, "previous_value": previous, "new_value": value}


if __name__ == "__main__":
    host = os.environ.get("HOST", "127.0.0.1")
    port = int(os.environ.get("PORT", "3000"))
    mcp.run(transport="streamable-http", host=host, port=port, path="/mcp")
