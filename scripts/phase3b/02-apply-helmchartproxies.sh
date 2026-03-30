#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

MGMT_CTX="agentic-mgmt"

echo "=== Phase 3b: Apply HelmChartProxy CRs ==="
echo ""

# ── Apply all HelmChartProxy CRs to the management cluster ──
echo "Applying all HelmChartProxy CRs from platform/caaph/..."
kubectl --context "$MGMT_CTX" apply -f "$ROOT_DIR/platform/caaph/"
echo ""

# ── Wait for CAAPH to create HelmReleaseProxy resources ──
echo "Waiting for CAAPH to create HelmReleaseProxy resources (up to 3 min)..."
DEADLINE=$((SECONDS + 180))
while true; do
  HRP_COUNT=$(kubectl --context "$MGMT_CTX" get helmreleaseproxy -A \
    --no-headers 2>/dev/null | wc -l | tr -d ' ')
  if [[ "$HRP_COUNT" -gt 0 ]]; then
    echo "  HelmReleaseProxy resources found: $HRP_COUNT"
    break
  fi
  if [[ $SECONDS -ge $DEADLINE ]]; then
    echo "  WARNING: No HelmReleaseProxy resources after 3 min."
    echo "  CAAPH may still be reconciling. Check:"
    echo "    kubectl --context $MGMT_CTX get helmreleaseproxy -A"
    break
  fi
  echo "  Waiting... (${SECONDS}s elapsed)"
  sleep 15
done

echo ""

# ── Show HelmReleaseProxy status per cluster ──
echo "── HelmReleaseProxy status ──"
kubectl --context "$MGMT_CTX" get helmreleaseproxy -A \
  -o custom-columns="NAMESPACE:.metadata.namespace,NAME:.metadata.name,CLUSTER:.spec.clusterRef.name,READY:.status.conditions[?(@.type=='Ready')].status" \
  2>/dev/null || kubectl --context "$MGMT_CTX" get helmreleaseproxy -A

echo ""

# ── Check releases are being installed on target clusters ──
echo "── Checking Helm releases on target clusters ──"

for CTX in agentic-obs agentic-cp agentic-cell-1 agentic-cell-2; do
  echo ""
  echo "  $CTX:"
  # Try to reach the cluster; it may not be reachable yet if CAAPH hasn't fully reconciled
  if kubectl --context "$CTX" cluster-info > /dev/null 2>&1; then
    kubectl --context "$CTX" get helmrelease -A --no-headers 2>/dev/null \
      | awk '{printf "    %-30s %-20s %s\n", $2, $1, $4}' || true
    # Also check monitoring namespace
    kubectl --context "$CTX" -n monitoring get pods --no-headers 2>/dev/null \
      | awk '{printf "    pod: %-40s %s\n", $1, $3}' || true
  else
    echo "    (cluster not reachable yet — CAAPH may still be bootstrapping)"
  fi
done

echo ""
echo "=== HelmChartProxies applied ==="
echo ""
echo "CAAPH will install:"
echo "  obs cluster   : cert-manager, aws-lb-controller, vm-operator, grafana, kiali-operator"
echo "  all clusters  : vmagent"
echo ""
echo "Monitor progress:"
echo "  kubectl --context $MGMT_CTX get helmreleaseproxy -A -w"
echo "  kubectl --context agentic-obs -n monitoring get pods -w"
echo ""
echo "Typical reconciliation time: 5-10 min per cluster."
echo "Proceed to 03-apply-obs-manifests.sh once vm-operator is running on agentic-obs."
