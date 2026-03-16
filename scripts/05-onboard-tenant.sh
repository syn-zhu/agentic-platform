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

# ── Create OpenFGA store and write authorization model ──
# Each tenant gets an isolated OpenFGA store. The authorization model is
# platform-defined (same for all tenants) and written during onboarding.
echo ""
echo "Creating OpenFGA store..."

OPENFGA_URL="http://openfga.openfga.svc.cluster.local:8080"
OPENFGA_MODEL="$ROOT_DIR/platform/manifests/openfga-model.json"

# Create store (or find existing)
EXISTING_STORE_ID=$(kubectl run openfga-curl-$RANDOM --rm -i --restart=Never \
  --image=curlimages/curl:latest -n openfga \
  -- curl -sf "$OPENFGA_URL/stores" 2>/dev/null \
  | python3 -c "
import sys, json
stores = json.load(sys.stdin).get('stores', [])
matches = [s['id'] for s in stores if s.get('name') == '${TENANT_NAME}']
print(matches[0] if matches else '')
" 2>/dev/null || echo "")

if [[ -n "$EXISTING_STORE_ID" ]]; then
  echo "  Store '$TENANT_NAME' already exists: $EXISTING_STORE_ID"
  OPENFGA_STORE_ID="$EXISTING_STORE_ID"
else
  STORE_RESPONSE=$(kubectl run openfga-curl-$RANDOM --rm -i --restart=Never \
    --image=curlimages/curl:latest -n openfga \
    -- curl -sf -X POST "$OPENFGA_URL/stores" \
       -H "Content-Type: application/json" \
       -d "{\"name\": \"${TENANT_NAME}\"}" 2>/dev/null)
  OPENFGA_STORE_ID=$(echo "$STORE_RESPONSE" | python3 -c "import sys,json; print(json.load(sys.stdin).get('id',''))" 2>/dev/null)

  if [[ -z "$OPENFGA_STORE_ID" ]]; then
    echo "  WARNING: Failed to create OpenFGA store. Tool-level access policies will not work."
    echo "  Response: $STORE_RESPONSE"
  else
    echo "  Store created: $OPENFGA_STORE_ID"
  fi
fi

if [[ -n "$OPENFGA_STORE_ID" ]]; then
  echo "Writing authorization model..."
  # Create a ConfigMap with the model JSON to mount into the curl pod
  kubectl create configmap openfga-model-json \
    --namespace openfga \
    --from-file=model.json="$OPENFGA_MODEL" \
    --dry-run=client -o yaml | kubectl apply -f -

  MODEL_RESPONSE=$(kubectl run openfga-model-$RANDOM --rm -i --restart=Never \
    --image=curlimages/curl:latest -n openfga \
    --overrides='{
      "spec": {
        "volumes": [{"name": "model", "configMap": {"name": "openfga-model-json"}}],
        "containers": [{
          "name": "curl",
          "image": "curlimages/curl:latest",
          "command": ["curl", "-sf", "-X", "POST",
            "-H", "Content-Type: application/json",
            "-d", "@/model/model.json",
            "'"$OPENFGA_URL/stores/$OPENFGA_STORE_ID/authorization-models"'"],
          "volumeMounts": [{"name": "model", "mountPath": "/model", "readOnly": true}]
        }]
      }
    }' 2>/dev/null)

  MODEL_ID=$(echo "$MODEL_RESPONSE" | python3 -c "import sys,json; print(json.load(sys.stdin).get('authorization_model_id',''))" 2>/dev/null)
  if [[ -n "$MODEL_ID" ]]; then
    echo "  Authorization model written: $MODEL_ID"
  else
    echo "  WARNING: Failed to write authorization model."
    echo "  Response: $MODEL_RESPONSE"
  fi

  # Store the store ID as a secret in the tenant namespace for use by policies
  kubectl create secret generic openfga-store \
    --namespace "$TENANT_NS" \
    --from-literal=store-id="$OPENFGA_STORE_ID" \
    --dry-run=client -o yaml | kubectl apply -f -
  echo "  Store ID saved as Secret 'openfga-store' in $TENANT_NS."
fi

echo ""
echo "=== Tenant '$TENANT_NAME' onboarded ==="
echo ""
echo "Namespace:  $TENANT_NS"
echo "Langfuse public key:  $TENANT_PK"
echo "OpenFGA store ID:  ${OPENFGA_STORE_ID:-not created}"
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
