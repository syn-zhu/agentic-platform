#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"
TEMPLATE_DIR="$ROOT_DIR/tenants/_template"

# ── Usage ──
if [[ $# -lt 1 ]]; then
  echo "Usage: $0 <tenant-name>"
  echo ""
  echo "Example: $0 alpha"
  exit 1
fi

TENANT_NAME="$1"
TENANT_NS="tenant-${TENANT_NAME}"

echo "=== Onboarding Tenant: $TENANT_NAME ==="
echo "Namespace: $TENANT_NS"

# ── Create materialized tenant directory ──
TENANT_DIR="$ROOT_DIR/tenants/onboarded/$TENANT_NS"
mkdir -p "$TENANT_DIR"

# ── Substitute template variables ──
echo "Generating tenant manifests from template..."
for TMPL in "$TEMPLATE_DIR"/*.yaml; do
  FILENAME=$(basename "$TMPL")
  sed \
    -e "s/{{ TENANT_NAME }}/$TENANT_NAME/g" \
    -e "s/{{ TENANT_NS }}/$TENANT_NS/g" \
    "$TMPL" > "$TENANT_DIR/$FILENAME"
done

# ── Apply namespace and RBAC first ──
echo "Applying namespace..."
kubectl apply -f "$TENANT_DIR/namespace.yaml"

echo "Applying RBAC..."
kubectl apply -f "$TENANT_DIR/rbac.yaml"

echo "Applying resource quota..."
kubectl apply -f "$TENANT_DIR/resource-quota.yaml"

echo "Applying network policy..."
kubectl apply -f "$TENANT_DIR/network-policy.yaml"

# ── Deploy per-tenant waypoint proxy (Istio Ambient Mesh L7) ──
echo "Deploying waypoint proxy..."
kubectl apply -f "$TENANT_DIR/waypoint.yaml"

# ── Apply Istio AuthorizationPolicy (L4 tenant isolation via ztunnel) ──
echo "Applying AuthorizationPolicy..."
kubectl apply -f "$TENANT_DIR/authz-policy.yaml"

# ── Create tenant secrets ──
echo "Creating tenant secrets..."

# Generate per-tenant Langfuse API keys
TENANT_PK="pk-lf-${TENANT_NAME}-$(openssl rand -hex 8)"
TENANT_SK="sk-lf-${TENANT_NAME}-$(openssl rand -hex 16)"
TENANT_BASIC_AUTH=$(echo -n "${TENANT_PK}:${TENANT_SK}" | base64 -w0 2>/dev/null || echo -n "${TENANT_PK}:${TENANT_SK}" | base64)

kubectl create secret generic langfuse-api-keys \
  --namespace "$TENANT_NS" \
  --from-literal=PUBLIC_KEY="$TENANT_PK" \
  --from-literal=SECRET_KEY="$TENANT_SK" \
  --from-literal=OTEL_AUTH_HEADER="Basic ${TENANT_BASIC_AUTH}" \
  --dry-run=client -o yaml | kubectl apply -f -

# ── Apply ModelConfig (no API key needed — gateway injects it) ──
echo "Applying ModelConfig..."
kubectl apply -f "$TENANT_DIR/modelconfig.yaml"

echo ""
echo "=== Tenant '$TENANT_NAME' onboarded ==="
echo ""
echo "Namespace:  $TENANT_NS"
echo "Langfuse public key:  $TENANT_PK"
echo ""
echo "The tenant can now deploy:"
echo "  - Agents (kagent.dev/v1alpha2 Agent)"
echo "  - MCP Servers (MCPServer / RemoteMCPServer)"
echo "  - AgentgatewayBackends (BYOK LLM providers)"
echo ""
echo "To expose resources through the gateway, add this annotation:"
echo "  platform.agentic.io/expose: \"true\""
echo ""
echo "Kyverno auto-generates HTTPRoutes with paths:"
echo "  /a2a/{namespace}/{name}  — A2A agents"
echo "  /llm/{namespace}/{name}  — LLM backends"
echo "  /mcp/{namespace}/{name}  — MCP backends"
echo ""
EXAMPLES_DIR="$ROOT_DIR/tenants/examples"
echo "Example manifests: $EXAMPLES_DIR/"
