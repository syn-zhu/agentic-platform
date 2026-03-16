#!/usr/bin/env bash
# Example 07: Write OpenFGA relationship tuples for the "acme" example.
#
# The OpenFGA store and authorization model are created during tenant
# onboarding (scripts/05-onboard-tenant.sh). This script writes the
# relationship tuples that represent:
#
#   1. Platform-managed tuples (normally written by controllers):
#      - Org membership       — synced from Keycloak Organizations
#      - Tool server -> org   — derived from namespace/deployment
#      - Tool hierarchy       — derived from MCP tools/list discovery
#
#   2. Tenant-managed tuples (the only thing a tenant actively decides):
#      - Role assignments     — who gets what role on which tool server
#
# Since none of those controllers exist yet, this script seeds all tuples
# manually so the example works end-to-end.
#
# Prerequisites:
#   - Tenant onboarded: scripts/05-onboard-tenant.sh acme
#   - OpenFGA server running: scripts/07-configure-openfga.sh
#   - kubectl access to the cluster
#   - curl and jq available locally
#
# Usage: ./setup-openfga.sh

set -euo pipefail

TENANT_NAME="acme"
TENANT_NS="tenant-${TENANT_NAME}"
LOCAL_PORT=18080

# ── Retrieve store ID from onboarding ─────────────────────────────────────────
echo "=== OpenFGA Example Setup: tenant '$TENANT_NAME' ==="
echo ""

STORE_ID=$(kubectl get secret openfga-store -n "$TENANT_NS" \
  -o jsonpath='{.data.store-id}' 2>/dev/null | base64 -d 2>/dev/null || echo "")

if [[ -z "$STORE_ID" ]]; then
  echo "ERROR: OpenFGA store ID not found in Secret '$TENANT_NS/openfga-store'."
  echo "  Run: scripts/05-onboard-tenant.sh $TENANT_NAME"
  exit 1
fi

echo "Store ID: $STORE_ID (from Secret $TENANT_NS/openfga-store)"
echo ""

# ── Port-forward to OpenFGA ──────────────────────────────────────────────────
echo "Setting up port-forward to OpenFGA..."

kubectl port-forward -n openfga svc/openfga "$LOCAL_PORT:8080" > /dev/null 2>&1 &
PF_PID=$!
trap 'kill $PF_PID 2>/dev/null || true' EXIT

# Wait for port-forward to be ready
for i in $(seq 1 10); do
  if curl -sf "http://localhost:${LOCAL_PORT}/stores" > /dev/null 2>&1; then
    break
  fi
  if [ "$i" -eq 10 ]; then
    echo "ERROR: Port-forward to OpenFGA failed after 10 attempts"
    exit 1
  fi
  sleep 1
done

OPENFGA="http://localhost:${LOCAL_PORT}"

# ── Helper ────────────────────────────────────────────────────────────────────

write_tuples() {
  local payload="$1"
  local description="$2"
  local response
  response=$(curl -sf -X POST "$OPENFGA/stores/$STORE_ID/write" \
    -H "Content-Type: application/json" \
    -d "$payload") || {
    echo "  ERROR: $description"
    echo "  $response"
    return 1
  }
  echo "  $description"
}

# ── 1. Platform-managed tuples (seed data) ────────────────────────────────────
# In production these would be written by platform controllers, not by hand.
echo "--- Step 1: Writing platform-managed tuples (seed data) ---"

# Org membership — normally synced from Keycloak Organizations
write_tuples '{
  "writes": {
    "tuple_keys": [
      {"user": "user:alice", "relation": "member", "object": "organization:acme"},
      {"user": "user:bob",   "relation": "member", "object": "organization:acme"}
    ]
  }
}' "Org membership: alice, bob -> organization:acme (source: Keycloak)"

# Tool server -> org — normally derived from namespace ownership
write_tuples '{
  "writes": {
    "tuple_keys": [
      {"user": "organization:acme", "relation": "org", "object": "tool_server:policy-mcp-server"}
    ]
  }
}' "Tool server org: organization:acme -> tool_server:policy-mcp-server (source: namespace)"

# Tool hierarchy — normally derived from MCP tools/list discovery
write_tuples '{
  "writes": {
    "tuple_keys": [
      {"user": "tool_server:policy-mcp-server", "relation": "parent", "object": "tool:policy-mcp-server/list_reports"},
      {"user": "tool_server:policy-mcp-server", "relation": "parent", "object": "tool:policy-mcp-server/read_report"},
      {"user": "tool_server:policy-mcp-server", "relation": "parent", "object": "tool:policy-mcp-server/execute_query"},
      {"user": "tool_server:policy-mcp-server", "relation": "parent", "object": "tool:policy-mcp-server/modify_config"}
    ]
  }
}' "Tool hierarchy: 4 tools -> parent -> tool_server:policy-mcp-server (source: tools/list)"

echo ""

# ── 2. Tenant-managed tuples (role assignments) ──────────────────────────────
# This is the only category a tenant admin actively manages.
echo "--- Step 2: Writing role assignments (tenant-managed) ---"

write_tuples '{
  "writes": {
    "tuple_keys": [
      {"user": "user:alice", "relation": "analyst",  "object": "tool_server:policy-mcp-server"},
      {"user": "user:bob",   "relation": "admin",    "object": "tool_server:policy-mcp-server"}
    ]
  }
}' "Role assignments: alice=analyst, bob=admin on tool_server:policy-mcp-server"

echo ""

# ── 3. Verify permissions ────────────────────────────────────────────────────
echo "--- Step 3: Verifying permissions ---"

PASS=0
FAIL=0

check_permission() {
  local user="$1" relation="$2" object="$3" expected="$4"
  local response allowed
  response=$(curl -sf -X POST "$OPENFGA/stores/$STORE_ID/check" \
    -H "Content-Type: application/json" \
    -d "{\"tuple_key\": {\"user\": \"$user\", \"relation\": \"$relation\", \"object\": \"$object\"}}")

  allowed=$(echo "$response" | jq -r '.allowed')
  if [[ "$allowed" == "$expected" ]]; then
    echo "  PASS: $user $relation $object -> $allowed"
    PASS=$((PASS + 1))
  else
    echo "  FAIL: $user $relation $object -> $allowed (expected $expected)"
    FAIL=$((FAIL + 1))
  fi
}

# alice (analyst) can call all tools
check_permission "user:alice" "can_call" "tool:policy-mcp-server/list_reports"  "true"
check_permission "user:alice" "can_call" "tool:policy-mcp-server/read_report"   "true"
check_permission "user:alice" "can_call" "tool:policy-mcp-server/execute_query" "true"
check_permission "user:alice" "can_call" "tool:policy-mcp-server/modify_config" "true"

# bob (admin) can call everything
check_permission "user:bob" "can_call" "tool:policy-mcp-server/list_reports"  "true"
check_permission "user:bob" "can_call" "tool:policy-mcp-server/modify_config" "true"

# unknown user should be denied
check_permission "user:eve" "can_call" "tool:policy-mcp-server/list_reports" "false"

echo ""
echo "Results: $PASS passed, $FAIL failed"

if [[ $FAIL -gt 0 ]]; then
  echo "WARNING: Some permission checks failed!"
fi

echo ""
echo "=== OpenFGA example setup complete ==="
echo ""
echo "Store ID: $STORE_ID"
echo "Use this store_id in the ext_authz policy: x-fga-store-id: '$STORE_ID'"
