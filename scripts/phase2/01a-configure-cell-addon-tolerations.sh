#!/usr/bin/env bash
set -euo pipefail

# Configures EKS addon tolerations on cell clusters so CoreDNS, ebs-csi-controller,
# and kube-proxy can schedule on tainted nodes. Cell clusters have all nodes tainted
# (workload, waypoint, gateway), so system addons need to tolerate all taints.
#
# CAPA's addon reconciler has a chicken-and-egg issue: it waits for addons to be
# ACTIVE before applying configuration, but the addons can't become active without
# tolerations. This script breaks the cycle by applying configuration directly.

REGION="us-east-1"
CELL_CLUSTERS=("agentic-cell-1" "agentic-cell-2")

echo "=== Configuring addon tolerations on cell clusters ==="

for CLUSTER in "${CELL_CLUSTERS[@]}"; do
  echo ""
  echo "── $CLUSTER ──"

  for ADDON in coredns kube-proxy; do
    echo "  Updating $ADDON tolerations..."
    aws eks update-addon \
      --cluster-name "$CLUSTER" \
      --addon-name "$ADDON" \
      --configuration-values '{"tolerations":[{"operator":"Exists"}]}' \
      --resolve-conflicts OVERWRITE \
      --region "$REGION" 2>&1 | grep -o '"status": "[^"]*"' || echo "    (submitted)"
  done

  echo "  Updating aws-ebs-csi-driver tolerations..."
  aws eks update-addon \
    --cluster-name "$CLUSTER" \
    --addon-name aws-ebs-csi-driver \
    --configuration-values '{"node":{"tolerations":[{"operator":"Exists"}]}}' \
    --resolve-conflicts OVERWRITE \
    --region "$REGION" 2>&1 | grep -o '"status": "[^"]*"' || echo "    (submitted)"

  echo "  Done."
done

echo ""
echo "Waiting 60s for addons to update..."
sleep 60

echo ""
echo "=== Verifying ==="
for CLUSTER in "${CELL_CLUSTERS[@]}"; do
  echo "── $CLUSTER ──"
  for ADDON in coredns kube-proxy aws-ebs-csi-driver; do
    CONFIG=$(aws eks describe-addon --cluster-name "$CLUSTER" --addon-name "$ADDON" \
      --region "$REGION" --query 'addon.configurationValues' --output text 2>/dev/null)
    STATUS=$(aws eks describe-addon --cluster-name "$CLUSTER" --addon-name "$ADDON" \
      --region "$REGION" --query 'addon.status' --output text 2>/dev/null)
    echo "  $ADDON: status=$STATUS config=$CONFIG"
  done
done
