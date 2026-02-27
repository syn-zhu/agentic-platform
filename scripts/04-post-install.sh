#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

echo "=== Phase 4: Applying Post-Install Manifests ==="

# Load .env (all platform config: user vars + AWS outputs + generated secrets)
if [[ -f "$ROOT_DIR/.env" ]]; then
  set -a; source "$ROOT_DIR/.env"; set +a
fi

# ── 1. Namespace labels (Kyverno policies match on these) ──
echo "Applying namespace labels..."
kubectl apply -f "$ROOT_DIR/platform/manifests/namespaces.yaml"

# ── 2. AgentgatewayParameters (tracing via gRPC → OTEL collector → Langfuse) ──
echo "Applying AgentgatewayParameters..."
kubectl apply -f "$ROOT_DIR/platform/manifests/agentgateway-parameters.yaml"

echo "Applying AgentgatewayParameters (waypoint)..."
kubectl apply -f "$ROOT_DIR/platform/manifests/agentgateway-waypoint-parameters.yaml"

# ── 3. Anthropic LLM Backend ──
echo "Applying Anthropic LLM backend..."
ANTHROPIC_KEY="${ANTHROPIC_API_KEY:-}"
if [[ -n "$ANTHROPIC_KEY" ]]; then
  sed "s|<ANTHROPIC_API_KEY>|${ANTHROPIC_KEY}|g" \
    "$ROOT_DIR/platform/manifests/anthropic-backend.yaml" | kubectl apply -f -
else
  echo "  WARNING: ANTHROPIC_API_KEY not set — skipping backend Secret"
  kubectl apply -f "$ROOT_DIR/platform/manifests/anthropic-backend.yaml"
fi

# ── 4. AgentGateway Proxy ──
echo "Applying AgentGateway Proxy..."
kubectl apply -f "$ROOT_DIR/platform/manifests/agentgateway-proxy.yaml"

# ── 5. Platform RBAC ──
echo "Applying platform RBAC..."
kubectl apply -f "$ROOT_DIR/platform/manifests/platform-rbac.yaml"

# ── 6. OTEL Collector (gRPC-to-HTTP bridge for kagent + agentgateway → Langfuse) ──
# NOTE: otel-collector-auth secret is created by 02-create-secrets.sh — not recreated here.
echo "Applying OTEL Collector..."
kubectl apply -f "$ROOT_DIR/platform/manifests/otel-collector.yaml"

# ── 7. Agent Sandbox (isolated agent runtimes) ──
AGENT_SANDBOX_VERSION="v0.1.1"
echo "Installing Agent Sandbox ${AGENT_SANDBOX_VERSION}..."
kubectl apply -f "https://github.com/kubernetes-sigs/agent-sandbox/releases/download/${AGENT_SANDBOX_VERSION}/manifest.yaml"
kubectl apply -f "https://github.com/kubernetes-sigs/agent-sandbox/releases/download/${AGENT_SANDBOX_VERSION}/extensions.yaml"

# ── 8. Sandbox Router ──
echo "Applying Sandbox Router..."
kubectl apply -f "$ROOT_DIR/platform/manifests/sandbox-router.yaml"
kubectl apply -f "$ROOT_DIR/platform/manifests/sandbox-router-route.yaml"

# ── 9. Kyverno policies ──
echo "Applying Kyverno policies..."
kubectl apply -f "$ROOT_DIR/platform/manifests/kyverno-tenant-scheduling.yaml"
kubectl apply -f "$ROOT_DIR/platform/manifests/kyverno-auto-expose.yaml"

# ── 10. AgentRegistry ──
echo "Deploying AgentRegistry..."
kubectl apply -f "$ROOT_DIR/platform/manifests/agentregistry.yaml"
echo "Waiting for AgentRegistry to be ready..."
kubectl rollout status deployment/agentregistry -n agentregistry --timeout=120s

# ── 11. AgentRegistry RemoteMCPServer (shared catalog tools) ──
echo "Applying AgentRegistry RemoteMCPServer..."
kubectl apply -f "$ROOT_DIR/platform/manifests/agentregistry-remotemcpserver.yaml"

# ── 12. Grafana service account + MCP token ──
echo "Setting up Grafana service account for MCP..."
GRAFANA_SVC="kube-prometheus-stack-grafana.monitoring.svc.cluster.local"
GRAFANA_INTERNAL="http://admin:admin@${GRAFANA_SVC}"

# Wait for Grafana to be ready (it may take a moment after helmfile sync)
echo "  Waiting for Grafana to be ready..."
kubectl rollout status deployment/kube-prometheus-stack-grafana -n monitoring --timeout=120s >/dev/null 2>&1

# Create (or reuse) the service account via the Grafana API
SA_RESPONSE=$(kubectl run grafana-sa-setup --rm -i --restart=Never \
  --image=curlimages/curl:latest -n monitoring \
  -- curl -s -X POST "${GRAFANA_INTERNAL}/api/serviceaccounts" \
  -H "Content-Type: application/json" \
  -d '{"name":"mcp-grafana","role":"Admin"}' 2>/dev/null || true)

SA_ID=$(echo "$SA_RESPONSE" | python3 -c "import sys,json; print(json.load(sys.stdin).get('id',''))" 2>/dev/null || true)

if [[ -z "$SA_ID" ]]; then
  # Service account may already exist — look it up
  SA_ID=$(kubectl run grafana-sa-lookup --rm -i --restart=Never \
    --image=curlimages/curl:latest -n monitoring \
    -- curl -s "${GRAFANA_INTERNAL}/api/serviceaccounts/search?query=mcp-grafana" \
    -H "Content-Type: application/json" 2>/dev/null \
    | python3 -c "import sys,json; sas=json.load(sys.stdin).get('serviceAccounts',[]); print(sas[0]['id'] if sas else '')" 2>/dev/null || true)
fi

if [[ -n "$SA_ID" ]]; then
  # Generate a token for this service account
  TOKEN_RESPONSE=$(kubectl run grafana-token-gen --rm -i --restart=Never \
    --image=curlimages/curl:latest -n monitoring \
    -- curl -s -X POST "${GRAFANA_INTERNAL}/api/serviceaccounts/${SA_ID}/tokens" \
    -H "Content-Type: application/json" \
    -d '{"name":"mcp-token"}' 2>/dev/null || true)

  TOKEN_KEY=$(echo "$TOKEN_RESPONSE" | python3 -c "import sys,json; print(json.load(sys.stdin).get('key',''))" 2>/dev/null || true)

  if [[ -n "$TOKEN_KEY" ]]; then
    # Create (or update) the secret that grafana-mcp needs
    kubectl create secret generic grafana-mcp-token \
      --namespace kagent-system \
      --from-literal=GRAFANA_SERVICE_ACCOUNT_TOKEN="$TOKEN_KEY" \
      --dry-run=client -o yaml | kubectl apply -f -
    echo "  grafana-mcp-token secret created in kagent-system"

    # Restart grafana-mcp so it picks up the secret
    kubectl rollout restart deployment/kagent-grafana-mcp -n kagent-system 2>/dev/null || true
  else
    echo "  WARNING: Could not generate Grafana token (response: $TOKEN_RESPONSE)"
  fi
else
  echo "  WARNING: Could not create/find Grafana service account (response: $SA_RESPONSE)"
fi

# ── 13. EverMemOS (long-term memory system) ──
# Secrets are created by 02-create-secrets.sh; manifest has no placeholders.
echo "Deploying EverMemOS..."
kubectl apply -f "$ROOT_DIR/platform/manifests/evermemos.yaml"
echo "Waiting for EverMemOS infrastructure to be ready..."
kubectl rollout status statefulset/evermemos-mongodb -n evermemos --timeout=120s
kubectl rollout status statefulset/evermemos-elasticsearch -n evermemos --timeout=180s
kubectl rollout status statefulset/evermemos-milvus -n evermemos --timeout=180s
kubectl rollout status deployment/evermemos-redis -n evermemos --timeout=60s
echo "Waiting for EverMemOS application to be ready..."
kubectl rollout status deployment/evermemos -n evermemos --timeout=180s

# ── 14. EverMemOS AgentGateway Waypoint ──
echo "Applying EverMemOS waypoint gateway..."
kubectl apply -f "$ROOT_DIR/platform/manifests/evermemos-gateway.yaml"

echo ""
echo "=== Post-install manifests applied ==="
echo ""
echo "Verify:"
echo "  kubectl get agentgatewayparameters -n agentgateway-system"
echo "  kubectl get gateways -n agentgateway-system"
echo "  kubectl get httproute -A -l platform.agentic.io/auto-generated=true"
echo "  kubectl get clusterroles | grep tenant"
echo "  kubectl get crds | grep agents.x-k8s.io"
echo "  kubectl get pods -n agent-sandbox-system"
echo "  kubectl get pods -n agentregistry"
echo "  kubectl get remotemcpservers -n kagent-system agentregistry"
echo "  kubectl get pods -n kagent-system -l app.kubernetes.io/name=grafana-mcp"
echo "  kubectl get pods -n evermemos"
echo "  kubectl get gateway -n evermemos"
echo ""
echo "Access UIs: ./port-forward.sh"
echo ""
echo "To onboard a tenant: ./05-onboard-tenant.sh <tenant-name>"
