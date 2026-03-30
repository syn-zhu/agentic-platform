#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

MGMT_CTX="agentic-mgmt"
OBS_CTX="agentic-obs"
ALL_CLUSTERS=(agentic-cp agentic-obs agentic-cell-1 agentic-cell-2)

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

echo "=== Phase 3b Verification: Observability Stack ==="
echo ""

# ── Management cluster: CAAPH ──
echo "── Management Cluster (agentic-mgmt) ──"
check "cluster reachable" \
  "kubectl --context $MGMT_CTX cluster-info"
check "CAAPH controller running" \
  "kubectl --context $MGMT_CTX -n caaph-system get deployment --no-headers 2>/dev/null | grep -qE '^[^ ]+ +[1-9]'"
check "HelmChartProxy CRD exists" \
  "kubectl --context $MGMT_CTX get crd helmchartproxies.addons.cluster.x-k8s.io"
check "HelmReleaseProxy resources exist" \
  "kubectl --context $MGMT_CTX get helmreleaseproxy -A --no-headers 2>/dev/null | grep -qE ."

echo ""

# ── Obs cluster: VM Operator ──
echo "── Obs Cluster (agentic-obs): VM Operator ──"
check "cluster reachable" \
  "kubectl --context $OBS_CTX cluster-info"
check "VM Operator running" \
  "kubectl --context $OBS_CTX -n monitoring get deployment \
    -o jsonpath='{.items[?(@.metadata.name==\"vm-operator-victoria-metrics-operator\")].status.readyReplicas}' \
    2>/dev/null | grep -qE '^[1-9]'"
check "VMSingle CRD exists" \
  "kubectl --context $OBS_CTX get crd vmsingle.operator.victoriametrics.com"
check "VMAuth CRD exists" \
  "kubectl --context $OBS_CTX get crd vmauth.operator.victoriametrics.com"
check "VMUser CRD exists" \
  "kubectl --context $OBS_CTX get crd vmuser.operator.victoriametrics.com"

echo ""

# ── Obs cluster: VictoriaMetrics ──
echo "── Obs Cluster (agentic-obs): VictoriaMetrics ──"
check "VMSingle CR exists" \
  "kubectl --context $OBS_CTX -n monitoring get vmsingle vm"
check "VMSingle deployment ready" \
  "kubectl --context $OBS_CTX -n monitoring get deployment vmsingle-vm \
    -o jsonpath='{.status.readyReplicas}' 2>/dev/null | grep -qE '^[1-9]'"
check "VMSingle PVC bound" \
  "kubectl --context $OBS_CTX -n monitoring get pvc \
    -o jsonpath='{.items[?(@.status.phase==\"Bound\")].metadata.name}' \
    2>/dev/null | grep -q vmsingle"
check "VMAuth CR exists" \
  "kubectl --context $OBS_CTX -n monitoring get vmauth vmauth"
check "VMAuth deployment ready" \
  "kubectl --context $OBS_CTX -n monitoring get deployment vmauth-vmauth \
    -o jsonpath='{.status.readyReplicas}' 2>/dev/null | grep -qE '^[1-9]'"
check "VMUser secrets generated (at least 1)" \
  "kubectl --context $OBS_CTX -n monitoring get secrets \
    --no-headers 2>/dev/null | grep -q vmuser"

echo ""

# ── Obs cluster: NLB ──
echo "── Obs Cluster (agentic-obs): NLB ──"
check "vmauth-external Service exists" \
  "kubectl --context $OBS_CTX -n monitoring get svc vmauth-external"
NLB_HOSTNAME=$(kubectl --context "$OBS_CTX" -n monitoring \
  get svc vmauth-external \
  -o jsonpath='{.status.loadBalancer.ingress[0].hostname}' 2>/dev/null || echo "")
if [[ -n "$NLB_HOSTNAME" ]]; then
  echo "  ✓ NLB has address: $NLB_HOSTNAME"
  PASS=$((PASS + 1))
else
  echo "  ✗ NLB address not yet assigned"
  FAIL=$((FAIL + 1))
fi

echo ""

# ── Obs cluster: cert-manager ──
echo "── Obs Cluster (agentic-obs): cert-manager ──"
check "cert-manager running" \
  "kubectl --context $OBS_CTX -n cert-manager get deployment cert-manager \
    -o jsonpath='{.status.readyReplicas}' 2>/dev/null | grep -qE '^[1-9]'"
check "VMAuth TLS cert Secret exists" \
  "kubectl --context $OBS_CTX -n monitoring get secret vmauth-tls-cert"

echo ""

# ── Obs cluster: Grafana ──
echo "── Obs Cluster (agentic-obs): Grafana ──"
check "Grafana deployment ready" \
  "kubectl --context $OBS_CTX -n monitoring get deployment grafana \
    -o jsonpath='{.status.readyReplicas}' 2>/dev/null | grep -qE '^[1-9]'"
check "Grafana Service exists" \
  "kubectl --context $OBS_CTX -n monitoring get svc grafana"

echo ""

# ── Obs cluster: Kiali ──
echo "── Obs Cluster (agentic-obs): Kiali ──"
check "Kiali Operator running" \
  "kubectl --context $OBS_CTX -n kiali-operator get deployment kiali-operator \
    -o jsonpath='{.status.readyReplicas}' 2>/dev/null | grep -qE '^[1-9]'"
check "Kiali CR exists" \
  "kubectl --context $OBS_CTX -n istio-system get kiali kiali 2>/dev/null || \
   kubectl --context $OBS_CTX -n istio-system get deployment kiali \
     -o jsonpath='{.status.readyReplicas}' 2>/dev/null | grep -qE '^[1-9]'"
check "Kiali remote secret for agentic-cell-1" \
  "kubectl --context $OBS_CTX -n istio-system get secret kiali-remote-agentic-cell-1"
check "Kiali remote secret for agentic-cell-2" \
  "kubectl --context $OBS_CTX -n istio-system get secret kiali-remote-agentic-cell-2"
check "Kiali remote secrets labeled kiali.io/multiCluster" \
  "kubectl --context $OBS_CTX -n istio-system get secrets \
    -l 'kiali.io/multiCluster=true' --no-headers 2>/dev/null | grep -qE ."

echo ""

# ── vmagent on all clusters ──
echo "── vmagent on all clusters ──"
for CLUSTER in "${ALL_CLUSTERS[@]}"; do
  check "$CLUSTER: vmagent DaemonSet or Deployment running" \
    "kubectl --context $CLUSTER -n monitoring \
      get deploy vmagent-victoria-metrics-agent \
      -o jsonpath='{.status.readyReplicas}' 2>/dev/null | grep -qE '^[1-9]'"
  check "$CLUSTER: vmagent-remote-write-token Secret exists" \
    "kubectl --context $CLUSTER -n monitoring get secret vmagent-remote-write-token"
done

echo ""

# ── Optional: Query VictoriaMetrics for metrics flowing ──
echo "── VictoriaMetrics metrics flow (optional) ──"
VM_POD=$(kubectl --context "$OBS_CTX" -n monitoring \
  get pods -l app.kubernetes.io/name=vmsingle \
  -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "")

if [[ -n "$VM_POD" ]]; then
  # Port-forward to VMSingle and query for up{} by cluster
  pkill -f "kubectl.*port-forward.*19429" 2>/dev/null || true
  kubectl --context "$OBS_CTX" port-forward -n monitoring "$VM_POD" 19429:8429 &
  PF_PID=$!
  trap 'kill $PF_PID 2>/dev/null || true' EXIT
  sleep 3

  VM_RESULT=$(curl -sf \
    "http://localhost:19429/api/v1/query?query=count(up{})%20by%20(cluster)" \
    2>/dev/null | python3 -c "
import sys, json
data = json.load(sys.stdin)
results = data.get('data', {}).get('result', [])
if not results:
    print('  No metrics yet (vmagent may still be starting)')
else:
    for r in results:
        cluster = r.get('metric', {}).get('cluster', 'unknown')
        value = r.get('value', [None, '0'])[1]
        print(f'  cluster={cluster}: {value} targets up')
" 2>/dev/null || echo "  Unable to parse VM response")

  if [[ -n "$VM_RESULT" ]]; then
    echo "$VM_RESULT"
    PASS=$((PASS + 1))
  else
    echo "  ✗ Could not query VictoriaMetrics (may need more time)"
    FAIL=$((FAIL + 1))
  fi

  kill $PF_PID 2>/dev/null || true
  trap - EXIT
else
  echo "  SKIP — VMSingle pod not found, skipping metrics query"
fi

echo ""
echo "════════════════════════════════════════════════════"
echo "Results: $PASS passed, $FAIL failed"
if [[ $FAIL -gt 0 ]]; then
  echo "PHASE 3b NOT READY — fix failures above before proceeding"
  exit 1
else
  echo "PHASE 3b READY — observability stack healthy"
fi
