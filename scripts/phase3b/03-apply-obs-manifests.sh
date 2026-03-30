#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

OBS_CTX="agentic-obs"

echo "=== Phase 3b: Apply Observability Manifests ==="
echo ""

# ── Wait for VM Operator CRDs to be available on the obs cluster ──
echo "Waiting for VictoriaMetrics Operator CRDs on $OBS_CTX (up to 10 min)..."
DEADLINE=$((SECONDS + 600))
VM_CRDS=(
  "vmsingle.operator.victoriametrics.com"
  "vmauth.operator.victoriametrics.com"
  "vmuser.operator.victoriametrics.com"
)
while true; do
  MISSING=0
  for CRD in "${VM_CRDS[@]}"; do
    if ! kubectl --context "$OBS_CTX" get crd "$CRD" > /dev/null 2>&1; then
      MISSING=$((MISSING + 1))
    fi
  done
  if [[ $MISSING -eq 0 ]]; then
    echo "  ✓ All VM Operator CRDs available"
    break
  fi
  if [[ $SECONDS -ge $DEADLINE ]]; then
    echo "  ERROR: VM Operator CRDs not ready after 10 min."
    echo "  Check CAAPH has installed vm-operator on $OBS_CTX:"
    echo "    kubectl --context $OBS_CTX -n monitoring get pods"
    echo "    kubectl --context agentic-mgmt get helmreleaseproxy -A | grep vm-operator"
    exit 1
  fi
  echo "  Waiting for CRDs... (${SECONDS}s elapsed, $MISSING CRDs still missing)"
  sleep 20
done

echo ""

# ── Apply all observability manifests to the obs cluster ──
echo "Applying observability manifests to $OBS_CTX..."
kubectl --context "$OBS_CTX" apply -f "$ROOT_DIR/platform/observability-manifests/"
echo "  ✓ Manifests applied"
echo ""

# ── Wait for VMSingle to be ready ──
echo "Waiting for VMSingle to become ready (up to 5 min)..."
DEADLINE=$((SECONDS + 300))
while true; do
  PHASE=$(kubectl --context "$OBS_CTX" -n monitoring get vmsingle vm \
    -o jsonpath='{.status.updateStatus}' 2>/dev/null || echo "")
  READY_REPLICAS=$(kubectl --context "$OBS_CTX" -n monitoring \
    get deployment vmsingle-vm \
    -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo "0")
  if [[ "$PHASE" == "operational" ]] || [[ "${READY_REPLICAS:-0}" -ge 1 ]]; then
    echo "  ✓ VMSingle is ready (status: ${PHASE:-running})"
    break
  fi
  if [[ $SECONDS -ge $DEADLINE ]]; then
    echo "  WARNING: VMSingle not ready after 5 min. Continuing anyway."
    echo "  Check: kubectl --context $OBS_CTX -n monitoring get vmsingle vm"
    break
  fi
  echo "  Waiting for VMSingle... (${SECONDS}s elapsed)"
  sleep 20
done

echo ""

# ── Wait for VMAuth to be ready ──
echo "Waiting for VMAuth to become ready (up to 5 min)..."
DEADLINE=$((SECONDS + 300))
while true; do
  READY_REPLICAS=$(kubectl --context "$OBS_CTX" -n monitoring \
    get deployment vmauth-vmauth \
    -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo "0")
  if [[ "${READY_REPLICAS:-0}" -ge 1 ]]; then
    echo "  ✓ VMAuth is ready (readyReplicas: $READY_REPLICAS)"
    break
  fi
  if [[ $SECONDS -ge $DEADLINE ]]; then
    echo "  WARNING: VMAuth not ready after 5 min. Continuing anyway."
    echo "  Check: kubectl --context $OBS_CTX -n monitoring get vmauth vmauth"
    break
  fi
  echo "  Waiting for VMAuth... (${SECONDS}s elapsed)"
  sleep 20
done

echo ""

# ── Extract VMAuth NLB DNS name ──
echo "── VMAuth NLB endpoint ──"
NLB_HOSTNAME=""
DEADLINE=$((SECONDS + 300))
while true; do
  NLB_HOSTNAME=$(kubectl --context "$OBS_CTX" -n monitoring \
    get svc vmauth-external \
    -o jsonpath='{.status.loadBalancer.ingress[0].hostname}' 2>/dev/null || echo "")
  if [[ -n "$NLB_HOSTNAME" ]]; then
    echo "  ✓ VMAuth NLB hostname: $NLB_HOSTNAME"
    break
  fi
  if [[ $SECONDS -ge $DEADLINE ]]; then
    echo "  WARNING: NLB hostname not assigned after 5 min."
    echo "  AWS may still be provisioning the NLB. Check:"
    echo "    kubectl --context $OBS_CTX -n monitoring get svc vmauth-external"
    NLB_HOSTNAME="PENDING"
    break
  fi
  echo "  Waiting for NLB hostname... (${SECONDS}s elapsed)"
  sleep 20
done

echo ""

# ── Extract VMUser generated bearer tokens ──
# The VM Operator stores generated passwords as Secrets:
#   Secret name: vmuser-<vmuser-name>  in the monitoring namespace
#   Key: password
echo "── Extracting VMUser bearer tokens ──"

declare -A CLUSTER_TOKEN
CLUSTERS=(agentic-cp agentic-obs agentic-cell-1 agentic-cell-2)

for CLUSTER in "${CLUSTERS[@]}"; do
  # Secret name: vmuser-cluster-<cluster-name>
  SECRET_NAME="vmuser-cluster-${CLUSTER}"
  TOKEN=$(kubectl --context "$OBS_CTX" -n monitoring \
    get secret "$SECRET_NAME" \
    -o jsonpath='{.data.password}' 2>/dev/null | base64 -d 2>/dev/null || echo "")
  if [[ -n "$TOKEN" ]]; then
    CLUSTER_TOKEN[$CLUSTER]="$TOKEN"
    echo "  ✓ Token extracted for $CLUSTER"
  else
    echo "  WARNING: Token not found for $CLUSTER (secret $SECRET_NAME not ready yet)"
    echo "    The VM Operator generates tokens asynchronously. Retry if needed:"
    echo "    kubectl --context $OBS_CTX -n monitoring get secrets | grep vmuser"
    CLUSTER_TOKEN[$CLUSTER]=""
  fi
done

echo ""

# ── Distribute bearer tokens to each cluster's monitoring namespace ──
echo "── Distributing bearer tokens to cluster monitoring namespaces ──"

for CLUSTER in "${CLUSTERS[@]}"; do
  CTX="$CLUSTER"
  TOKEN="${CLUSTER_TOKEN[$CLUSTER]:-}"

  if [[ -z "$TOKEN" ]]; then
    echo "  SKIP $CLUSTER — no token available (re-run script after VM Operator generates secrets)"
    continue
  fi

  # Ensure monitoring namespace exists on target cluster
  kubectl --context "$CTX" create namespace monitoring \
    --dry-run=client -o yaml | kubectl --context "$CTX" apply -f - > /dev/null 2>&1

  # Create/update the vmagent remote-write token Secret
  kubectl --context "$CTX" -n monitoring create secret generic vmagent-remote-write-token \
    --from-literal=token="$TOKEN" \
    --dry-run=client -o yaml | kubectl --context "$CTX" apply -f -

  echo "  ✓ Token distributed to $CTX (monitoring/vmagent-remote-write-token)"
done

echo ""

# ── Update vmagent HelmChartProxy with actual NLB endpoint ──
if [[ "$NLB_HOSTNAME" != "PENDING" && -n "$NLB_HOSTNAME" ]]; then
  echo "── Updating vmagent HelmChartProxy with NLB endpoint ──"
  VMAGENT_PROXY="$ROOT_DIR/platform/caaph/helmchartproxy-vmagent.yaml"

  if grep -q "PLACEHOLDER_VMAUTH_ENDPOINT" "$VMAGENT_PROXY"; then
    # Replace placeholder with actual hostname using sed (safe: only modifies placeholder)
    sed -i.bak "s|PLACEHOLDER_VMAUTH_ENDPOINT|${NLB_HOSTNAME}|g" "$VMAGENT_PROXY"
    rm -f "${VMAGENT_PROXY}.bak"
    echo "  ✓ Updated $VMAGENT_PROXY with NLB hostname"

    # Re-apply to trigger CAAPH reconciliation
    kubectl --context agentic-mgmt apply -f "$VMAGENT_PROXY"
    echo "  ✓ Re-applied vmagent HelmChartProxy to management cluster"
  else
    echo "  Placeholder already replaced — vmagent already configured"
  fi
else
  echo "  NLB hostname not yet available — update platform/caaph/helmchartproxy-vmagent.yaml"
  echo "  manually once the NLB is provisioned:"
  echo "    sed -i 's|PLACEHOLDER_VMAUTH_ENDPOINT|<nlb-hostname>|g' \\"
  echo "      platform/caaph/helmchartproxy-vmagent.yaml"
  echo "    kubectl --context agentic-mgmt apply -f platform/caaph/helmchartproxy-vmagent.yaml"
fi

echo ""
echo "=== Observability manifests applied ==="
echo ""
if [[ "$NLB_HOSTNAME" != "PENDING" && -n "$NLB_HOSTNAME" ]]; then
  echo "VMAuth endpoint: https://${NLB_HOSTNAME}/api/v1/write"
else
  echo "VMAuth endpoint: PENDING (NLB not yet provisioned)"
fi
echo ""
echo "Next: run 04-configure-kiali-remotes.sh"
