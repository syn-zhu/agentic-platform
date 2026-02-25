#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

echo "=== Phase 3: Deploying Platform via Helmfile ==="

cd "$ROOT_DIR/platform"

# Verify helmfile is installed
if ! command -v helmfile &>/dev/null; then
  echo "ERROR: helmfile is not installed. Install it from https://github.com/helmfile/helmfile"
  exit 1
fi

# Set environment (default: dev)
HELMFILE_ENV="${HELMFILE_ENV:-dev}"
echo "Environment: $HELMFILE_ENV"

# ── Install Kubernetes Gateway API CRDs (prerequisite for agentgateway) ──
GATEWAY_API_VERSION="v1.4.0"
echo "Installing Kubernetes Gateway API CRDs (${GATEWAY_API_VERSION})..."
kubectl apply -f "https://github.com/kubernetes-sigs/gateway-api/releases/download/${GATEWAY_API_VERSION}/standard-install.yaml"

# Sync all releases in dependency order
echo ""
echo "Running helmfile sync (this may take several minutes)..."
helmfile -e "$HELMFILE_ENV" sync

echo ""
echo "=== Platform deployment complete ==="
echo ""
echo "Verify deployments:"
echo "  kubectl get pods -n istio-system"
echo "  kubectl get pods -n langfuse"
echo "  kubectl get pods -n agentgateway-system"
echo "  kubectl get pods -n kagent-system"
echo "  kubectl get pods -n monitoring"
echo ""
echo "Next step: ./04-post-install.sh"
