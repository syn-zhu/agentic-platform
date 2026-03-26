#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

REGION="us-east-1"
PASS=0
FAIL=0

check() {
  local desc="$1"; shift
  if eval "$*" > /dev/null 2>&1; then
    echo "  ✓ $desc"
    PASS=$((PASS + 1))
  else
    echo "  ✗ $desc"
    FAIL=$((FAIL + 1))
  fi
}

echo "=== Phase 2 Verification ==="

echo ""
echo "── Control-Plane (agentic-cp) ──"
check "Cluster reachable" "kubectl --context agentic-cp cluster-info"
check "Nodes ready" "kubectl --context agentic-cp get nodes -o jsonpath='{.items[0].status.conditions[?(@.type==\"Ready\")].status}' | grep -q True"
check "istiod running (sidecar)" "kubectl --context agentic-cp -n istio-system get deployment istiod -o jsonpath='{.status.readyReplicas}' | grep -qE '[0-9]+'"
check "gp3 StorageClass" "kubectl --context agentic-cp get sc gp3"

echo ""
echo "── Observability (agentic-obs) ──"
check "Cluster reachable" "kubectl --context agentic-obs cluster-info"
check "Nodes ready" "kubectl --context agentic-obs get nodes -o jsonpath='{.items[0].status.conditions[?(@.type==\"Ready\")].status}' | grep -q True"
check "gp3 StorageClass" "kubectl --context agentic-obs get sc gp3"

for CELL in agentic-cell-1 agentic-cell-2; do
  echo ""
  echo "── $CELL ──"
  check "Cluster reachable" "kubectl --context $CELL cluster-info"
  check "Nodes ready" "kubectl --context $CELL get nodes -o jsonpath='{.items[0].status.conditions[?(@.type==\"Ready\")].status}' | grep -q True"
  check "istiod running (ambient)" "kubectl --context $CELL -n istio-system get deployment istiod -o jsonpath='{.status.readyReplicas}' | grep -qE '[0-9]+'"
  check "ztunnel running" "kubectl --context $CELL -n istio-system get daemonset ztunnel -o jsonpath='{.status.numberReady}' | grep -qE '[0-9]+'"
  check "istio-cni running" "kubectl --context $CELL -n istio-system get daemonset istio-cni-node -o jsonpath='{.status.numberReady}' | grep -qE '[0-9]+'"
  check "gp3 StorageClass" "kubectl --context $CELL get sc gp3"
done

echo ""
echo "── Transit Gateway ──"
TGW_ID=$(cat "$ROOT_DIR/cluster/transit-gateway/.tgw-id" 2>/dev/null || echo "")
if [[ -n "$TGW_ID" ]]; then
  ATTACHMENTS=$(aws ec2 describe-transit-gateway-vpc-attachments \
    --filters "Name=transit-gateway-id,Values=$TGW_ID" "Name=state,Values=available" \
    --region "$REGION" --query 'length(TransitGatewayVpcAttachments)' --output text)
  check "TGW has 5 VPC attachments (mgmt+cp+obs+cell1+cell2)" "test $ATTACHMENTS -ge 5"
else
  echo "  ✗ TGW not found"
  FAIL=$((FAIL + 1))
fi

echo ""
echo "════════════════════════════"
echo "Results: $PASS passed, $FAIL failed"
if [[ $FAIL -gt 0 ]]; then
  echo "PHASE 2 NOT READY — fix failures above"
  exit 1
else
  echo "PHASE 2 READY — proceed to Phase 3"
fi
