#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

CONTEXT="agentic-mgmt"
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

echo "=== Phase 1 Verification ==="
echo ""

echo "── Management Cluster ──"
check "Cluster reachable" "kubectl --context $CONTEXT cluster-info"
check "Nodes ready" "kubectl --context $CONTEXT get nodes -o jsonpath='{.items[0].status.conditions[?(@.type==\"Ready\")].status}' | grep -q True"
check "gp3 StorageClass exists" "kubectl --context $CONTEXT get sc gp3"

echo ""
echo "── cert-manager ──"
check "cert-manager running" "kubectl --context $CONTEXT -n cert-manager get deployment cert-manager -o jsonpath='{.status.readyReplicas}' | grep -qE '[0-9]+'"
check "cert-manager webhook ready" "kubectl --context $CONTEXT -n cert-manager get deployment cert-manager-webhook -o jsonpath='{.status.readyReplicas}' | grep -qE '[0-9]+'"
check "Root CA ClusterIssuer ready" "kubectl --context $CONTEXT get clusterissuer agentic-platform-root-ca -o jsonpath='{.status.conditions[0].status}' | grep -q True"
check "Root CA Certificate issued" "kubectl --context $CONTEXT -n cert-manager get certificate agentic-platform-root-ca -o jsonpath='{.status.conditions[0].status}' | grep -q True"

echo ""
echo "── Cluster API ──"
check "CAPI operator running" "kubectl --context $CONTEXT -n capi-system get deployment --no-headers | grep -q ."
check "CAPA provider available" "kubectl --context $CONTEXT get infrastructureprovider aws -n capa-system"

echo ""
echo "── Transit Gateway ──"
TGW_ID=$(cat "$ROOT_DIR/cluster/transit-gateway/.tgw-id" 2>/dev/null || echo "")
if [[ -n "$TGW_ID" ]]; then
  check "TGW exists and available" "aws ec2 describe-transit-gateways --transit-gateway-ids $TGW_ID --region $REGION --query 'TransitGateways[0].State' --output text | grep -q available"
  check "Management VPC attached" "aws ec2 describe-transit-gateway-vpc-attachments --filters 'Name=transit-gateway-id,Values=$TGW_ID' --region $REGION --query 'TransitGatewayVpcAttachments[0].State' --output text | grep -q available"
else
  echo "  ✗ TGW ID file not found — run 01-create-transit-gateway.sh"
  FAIL=$((FAIL + 2))
fi

echo ""
echo "── Test: Issue an intermediate CA ──"
TEST_CERT_NAME="istio-ca-test-cluster"
CLUSTER_NAME="test-cluster" envsubst '$CLUSTER_NAME' \
  < "$ROOT_DIR/platform/management-manifests/intermediate-ca-template.yaml" \
  | kubectl --context "$CONTEXT" apply -f - > /dev/null 2>&1
sleep 5
check "Intermediate CA issued" "kubectl --context $CONTEXT -n cert-manager get certificate $TEST_CERT_NAME -o jsonpath='{.status.conditions[0].status}' | grep -q True"
# Cleanup test cert
kubectl --context "$CONTEXT" -n cert-manager delete certificate "$TEST_CERT_NAME" > /dev/null 2>&1 || true
kubectl --context "$CONTEXT" -n cert-manager delete secret "$TEST_CERT_NAME" > /dev/null 2>&1 || true

echo ""
echo "════════════════════════════"
echo "Results: $PASS passed, $FAIL failed"
if [[ $FAIL -gt 0 ]]; then
  echo "FOUNDATION NOT READY — fix failures above before proceeding to Phase 2"
  exit 1
else
  echo "FOUNDATION READY — proceed to Phase 2"
fi
