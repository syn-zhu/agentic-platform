#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

echo "=== Phase 3b: Labeling clusters for CAAPH ==="

MGMT_CTX="agentic-mgmt"

# All clusters get vmagent
echo "Labeling clusters for vmagent..."
for CLUSTER in agentic-cp agentic-obs agentic-cell-1 agentic-cell-2; do
  kubectl --context "$MGMT_CTX" -n default label cluster "$CLUSTER" \
    monitoring=vmagent --overwrite
  echo "  $CLUSTER: monitoring=vmagent"
done

# Only obs cluster gets the full observability stack
echo "Labeling obs cluster for observability stack..."
kubectl --context "$MGMT_CTX" -n default label cluster agentic-obs \
  observability=true --overwrite
echo "  agentic-obs: observability=true"

# Cell clusters get kiali-remote label
echo "Labeling cell clusters for Kiali remote access..."
for CLUSTER in agentic-cell-1 agentic-cell-2; do
  kubectl --context "$MGMT_CTX" -n default label cluster "$CLUSTER" \
    kiali-remote=true --overwrite
  echo "  $CLUSTER: kiali-remote=true"
done

# Apply obs cluster namespaces
echo ""
echo "Applying obs cluster namespaces..."
kubectl --context agentic-obs apply -f "$ROOT_DIR/platform/observability-manifests/namespaces.yaml"

echo ""
echo "=== Labels applied ==="
