#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

echo "=== Phase 3b: Installing CAAPH ==="

MGMT_CTX="agentic-mgmt"

# Create namespace
kubectl --context "$MGMT_CTX" create namespace caaph-system \
  --dry-run=client -o yaml | kubectl --context "$MGMT_CTX" apply -f -

# Apply AddonProvider CR
echo "Creating CAAPH AddonProvider..."
kubectl --context "$MGMT_CTX" apply -f "$ROOT_DIR/platform/management-manifests/caaph-provider.yaml"

# Wait for CAAPH controller
echo "Waiting for CAAPH controller..."
sleep 30
kubectl --context "$MGMT_CTX" -n caaph-system wait deployment --all \
  --for=condition=Available --timeout=180s 2>/dev/null || {
  echo "  CAAPH controller still starting. Check: kubectl --context $MGMT_CTX -n caaph-system get pods"
}

# Verify HelmChartProxy CRD exists
echo "Verifying CRD..."
kubectl --context "$MGMT_CTX" get crd helmchartproxies.addons.cluster.x-k8s.io > /dev/null 2>&1 && \
  echo "  ✓ HelmChartProxy CRD available" || \
  echo "  ✗ HelmChartProxy CRD not found"

echo ""
echo "=== CAAPH installed ==="
