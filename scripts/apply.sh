#!/usr/bin/env bash
# apply.sh — Idempotent apply of the entire platform + tenants.
#
# Usage:
#   ./scripts/apply.sh                  # full apply (helmfile + manifests + tenants)
#   ./scripts/apply.sh --skip-helm      # skip helmfile sync (manifests + tenants only)
#
# Requires:
#   - AWS_PROFILE or valid kubeconfig context for EKS
#   - .env file with platform secrets (created by 01/02 setup scripts)
#   - helmfile, kubectl, helm installed
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

SKIP_HELM=false
for arg in "$@"; do
  case "$arg" in
    --skip-helm) SKIP_HELM=true ;;
    *) echo "Unknown flag: $arg"; exit 1 ;;
  esac
done

# ── Load .env ──
if [[ -f "$ROOT_DIR/.env" ]]; then
  set -a; source "$ROOT_DIR/.env"; set +a
fi

# ── Ensure AWS profile ──
export AWS_PROFILE="${AWS_PROFILE:-agentic-platform}"

echo "=== Applying Agentic Platform ==="
echo "Cluster: $(kubectl config current-context)"
echo ""

# ════════════════════════════════════════════════════
# 1. Helmfile sync (CRDs + controllers + observability)
# ════════════════════════════════════════════════════
if [[ "$SKIP_HELM" == false ]]; then
  echo "── Helmfile sync ──"
  HELMFILE_ENV="${HELMFILE_ENV:-dev}"

  # Gateway API CRDs (prerequisite)
  GATEWAY_API_VERSION="v1.4.0"
  echo "Installing Gateway API CRDs (${GATEWAY_API_VERSION})..."
  kubectl apply -f "https://github.com/kubernetes-sigs/gateway-api/releases/download/${GATEWAY_API_VERSION}/standard-install.yaml"

  echo "Running helmfile sync (env: $HELMFILE_ENV)..."
  cd "$ROOT_DIR/platform"
  helmfile -e "$HELMFILE_ENV" sync
  cd "$ROOT_DIR"
  echo ""
fi

# ════════════════════════════════════════════════════
# 2. Post-install manifests (platform-level)
# ════════════════════════════════════════════════════
echo "── Platform manifests ──"

# AgentgatewayParameters (Langfuse tracing)
BASIC_AUTH="${LANGFUSE_BASIC_AUTH:?ERROR: LANGFUSE_BASIC_AUTH not set in .env — run 02-create-secrets.sh first}"
echo "Applying AgentgatewayParameters..."
sed "s|<BASE64_PK_SK>|${BASIC_AUTH}|g" \
  "$ROOT_DIR/platform/manifests/agentgateway-parameters.yaml" | kubectl apply -f -

# Anthropic LLM Backend (substitute API key into secret)
echo "Applying Anthropic LLM backend..."
ANTHROPIC_KEY="${ANTHROPIC_API_KEY:-}"
if [[ -n "$ANTHROPIC_KEY" ]]; then
  sed "s|<ANTHROPIC_API_KEY>|${ANTHROPIC_KEY}|g" \
    "$ROOT_DIR/platform/manifests/anthropic-backend.yaml" | kubectl apply -f -
else
  echo "  ⚠ ANTHROPIC_API_KEY not set — applying without key substitution"
  kubectl apply -f "$ROOT_DIR/platform/manifests/anthropic-backend.yaml"
fi

# Static manifests (no substitution needed)
echo "Applying Gateway, RBAC, RemoteMCPServer..."
kubectl apply -f "$ROOT_DIR/platform/manifests/agentgateway-proxy.yaml"
kubectl apply -f "$ROOT_DIR/platform/manifests/platform-rbac.yaml"
kubectl apply -f "$ROOT_DIR/platform/manifests/platform-tools-remotemcpserver.yaml"

echo ""

# ════════════════════════════════════════════════════
# 3. Tenant manifests (all onboarded tenants)
# ════════════════════════════════════════════════════
echo "── Tenants ──"
TENANTS_DIR="$ROOT_DIR/tenants/onboarded"
if [[ -d "$TENANTS_DIR" ]]; then
  for TENANT_DIR in "$TENANTS_DIR"/*/; do
    TENANT_NS=$(basename "$TENANT_DIR")
    echo "Applying tenant: $TENANT_NS"

    # Apply core infrastructure in order
    CORE_FILES=(namespace.yaml rbac.yaml resource-quota.yaml network-policy.yaml modelconfig.yaml)
    for f in "${CORE_FILES[@]}"; do
      [[ -f "$TENANT_DIR/$f" ]] && kubectl apply -f "$TENANT_DIR/$f"
    done

    # Apply any additional tenant-specific resources (custom agents, backends, etc.)
    for f in "$TENANT_DIR"/*.yaml; do
      BASENAME=$(basename "$f")
      # Skip core files (already applied above) and non-resource files
      case "$BASENAME" in
        namespace.yaml|rbac.yaml|resource-quota.yaml|network-policy.yaml|modelconfig.yaml|kustomization.yaml) continue ;;
      esac
      kubectl apply -f "$f"
    done
  done
else
  echo "  No onboarded tenants found."
fi

echo ""
echo "=== Done ==="
