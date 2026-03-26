#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

echo "=== Phase 2.0: Provisioning Clusters via CAPA ==="

MGMT_CTX="agentic-mgmt"
REGION="us-east-1"
CLUSTER_DIRS=("control-plane" "observability" "cell-1" "cell-2")
EKS_NAMES=("agentic-cp" "agentic-obs" "agentic-cell-1" "agentic-cell-2")

# Verify management cluster
kubectl --context "$MGMT_CTX" cluster-info > /dev/null 2>&1 || {
  echo "ERROR: Cannot reach management cluster. Is Phase 1 complete?"
  exit 1
}

# Apply CAPA CRs for each cluster
for DIR in "${CLUSTER_DIRS[@]}"; do
  echo ""
  echo "── Provisioning: $DIR ──"
  kubectl --context "$MGMT_CTX" apply -f "$ROOT_DIR/cluster/$DIR/cluster.yaml"
  kubectl --context "$MGMT_CTX" apply -f "$ROOT_DIR/cluster/$DIR/machinepool.yaml"
done

# Wait for clusters to be ready
echo ""
echo "Waiting for clusters to be provisioned (this may take 15-20 minutes)..."
for NAME in "${EKS_NAMES[@]}"; do
  echo "  Waiting for $NAME..."
  kubectl --context "$MGMT_CTX" -n default wait cluster "$NAME" \
    --for=condition=Ready --timeout=1200s 2>/dev/null || {
    echo "  WARNING: $NAME not ready yet. Check: kubectl --context $MGMT_CTX get cluster $NAME"
  }
done

# Update kubeconfig for each cluster
echo ""
echo "Updating kubeconfig..."
for NAME in "${EKS_NAMES[@]}"; do
  aws eks update-kubeconfig --name "$NAME" --region "$REGION" --alias "$NAME" 2>/dev/null || \
    echo "  $NAME kubeconfig not yet available"
done

# Apply gp3 StorageClass on each cluster
for NAME in "${EKS_NAMES[@]}"; do
  echo "Creating gp3 StorageClass on $NAME..."
  kubectl --context "$NAME" apply -f "$ROOT_DIR/cluster/shared/storageclass-gp3.yaml" 2>/dev/null || true
done

echo ""
echo "=== Clusters provisioned: ${EKS_NAMES[*]} ==="
