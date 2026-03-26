#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

echo "=== Phase 2.2: Installing Istio ==="

# ── Control-plane: Istio sidecar mode ──
echo ""
echo "── Installing Istio sidecar on agentic-cp ──"

kubectl --context "agentic-cp" get crd gateways.gateway.networking.k8s.io > /dev/null 2>&1 || \
  kubectl --context "agentic-cp" apply --server-side \
    -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.4.0/experimental-install.yaml

cd "$ROOT_DIR/platform/control-plane"
helmfile --kube-context "agentic-cp" sync
cd "$ROOT_DIR"

kubectl --context "agentic-cp" -n istio-system rollout status deployment/istiod --timeout=120s
echo "  ✓ Istio sidecar installed on agentic-cp"

# ── Cells: Istio ambient mode ──
CELL_CLUSTERS=("agentic-cell-1" "agentic-cell-2")

for CLUSTER in "${CELL_CLUSTERS[@]}"; do
  echo ""
  echo "── Installing Istio ambient on $CLUSTER ──"

  kubectl --context "$CLUSTER" get crd gateways.gateway.networking.k8s.io > /dev/null 2>&1 || \
    kubectl --context "$CLUSTER" apply --server-side \
      -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.4.0/experimental-install.yaml

  cd "$ROOT_DIR/platform/cell"
  helmfile --kube-context "$CLUSTER" sync
  cd "$ROOT_DIR"

  kubectl --context "$CLUSTER" -n istio-system rollout status deployment/istiod --timeout=120s
  echo "  ✓ Istio ambient installed on $CLUSTER"
done

echo ""
echo "=== Istio installed ==="
echo "  agentic-cp: sidecar mode"
echo "  agentic-cell-1, agentic-cell-2: ambient mode"
echo "  agentic-obs: no mesh (intentional)"
