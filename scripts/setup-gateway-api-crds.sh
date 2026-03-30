#!/usr/bin/env bash
set -euo pipefail

# Declaratively installs Gateway API v1.4.0 experimental CRDs on all CAPA-managed
# clusters via ClusterResourceSet. The CRDs are stored as ConfigMaps on the
# management cluster and auto-applied to clusters with label gateway-api=v1.4.0.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

MGMT_CTX="agentic-mgmt"
MANIFESTS="$ROOT_DIR/platform/management-manifests"
GATEWAY_API_VERSION="v1.4.0"

echo "=== Setting up Gateway API CRDs via ClusterResourceSet ==="

# ── 1. Create ConfigMaps from the split CRD manifests ──
echo "Creating ConfigMaps from Gateway API CRD manifests..."

for PART in 0 1; do
  FILE="$MANIFESTS/gateway-api-part-${PART}.yaml"
  if [[ ! -f "$FILE" ]]; then
    echo "ERROR: $FILE not found. Download and split Gateway API CRDs first."
    exit 1
  fi

  kubectl --context "$MGMT_CTX" -n default create configmap "gateway-api-crds-part-${PART}" \
    --from-file="data=${FILE}" \
    --dry-run=client -o yaml | kubectl --context "$MGMT_CTX" apply --server-side -f -
  echo "  Created: gateway-api-crds-part-${PART}"
done

# ── 2. Apply the ClusterResourceSet ──
echo "Applying ClusterResourceSet..."
kubectl --context "$MGMT_CTX" apply -f "$MANIFESTS/gateway-api-clusterresourceset.yaml"

# ── 3. Label all clusters to trigger CRD application ──
echo "Labeling clusters with gateway-api=${GATEWAY_API_VERSION}..."
CLUSTERS=$(kubectl --context "$MGMT_CTX" get clusters -n default -o jsonpath='{.items[*].metadata.name}')
for CLUSTER in $CLUSTERS; do
  kubectl --context "$MGMT_CTX" -n default label cluster "$CLUSTER" \
    "gateway-api=${GATEWAY_API_VERSION}" --overwrite
  echo "  Labeled: $CLUSTER"
done

# ── 4. Verify ──
echo ""
echo "Waiting 30s for ClusterResourceSet to reconcile..."
sleep 30

echo "Checking Gateway API CRDs on each cluster..."
for CTX in agentic-cp agentic-obs agentic-cell-1 agentic-cell-2; do
  VER=$(kubectl --context "$CTX" get crd gateways.gateway.networking.k8s.io \
    -o jsonpath='{.metadata.annotations.gateway\.networking\.k8s\.io/bundle-version}' 2>/dev/null || echo "not found")
  echo "  $CTX: Gateway API $VER"
done

echo ""
echo "=== Gateway API CRDs setup complete ==="
