#!/usr/bin/env bash
set -euo pipefail

# Reinstalls CAPA cleanly, working around the CAPI Operator CRD CA race condition.
# The operator patches CRDs with a placeholder caBundle; cert-manager must inject
# the real CA before the operator validates. This script gives cert-manager enough
# time by splitting the install into two phases.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

CONTEXT="agentic-mgmt"

echo "=== Reinstalling CAPA (working around CRD CA race) ==="

# Step 1: Remove InfrastructureProvider and all CAPA CRDs
echo "Step 1: Cleaning CAPA state..."
kubectl --context "$CONTEXT" delete infrastructureprovider aws -n capa-system --timeout=15s 2>/dev/null || true
sleep 5

for CRD in $(kubectl --context "$CONTEXT" get crd -o name 2>&1 | grep -E '(infrastructure|controlplane\.cluster|bootstrap\.cluster)' | grep -v operator); do
  kubectl --context "$CONTEXT" patch "$CRD" --type=json -p='[{"op":"replace","path":"/metadata/finalizers","value":[]}]' 2>/dev/null || true
  kubectl --context "$CONTEXT" delete "$CRD" --timeout=10s 2>/dev/null || true
done

# Delete CAPA namespace to clear certs/secrets
kubectl --context "$CONTEXT" delete namespace capa-system --timeout=30s 2>/dev/null || true
sleep 10

# Step 2: Recreate namespace and configSecret (with correct variables)
echo "Step 2: Recreating prerequisites..."
kubectl --context "$CONTEXT" create namespace capa-system --dry-run=client -o yaml \
  | kubectl --context "$CONTEXT" apply -f -

ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
export CAPA_ROLE_ARN="arn:aws:iam::${ACCOUNT_ID}:role/agentic-mgmt-capa-controller"

envsubst '$CAPA_ROLE_ARN' \
  < "$ROOT_DIR/platform/control-plane-manifests/capa-variables-secret.yaml" \
  | kubectl --context "$CONTEXT" apply -f -

# Step 3: Create the InfrastructureProvider
echo "Step 3: Creating providers..."
kubectl --context "$CONTEXT" apply -f "$ROOT_DIR/platform/control-plane-manifests/capi-providers.yaml"

# Step 4: Wait for CRDs to be created, then wait for cert-manager to inject CA
echo "Step 4: Waiting for CRDs + cert-manager CA injection..."
echo "  Waiting 30s for CRDs to appear..."
sleep 30

echo "  Waiting 30s for cert-manager CA injector..."
sleep 30

# Step 5: Check if install succeeded or if we need to retry
STATUS=$(kubectl --context "$CONTEXT" get infrastructureprovider aws -n capa-system \
  -o jsonpath='{.status.conditions[?(@.type=="ProviderInstalled")].reason}' 2>/dev/null || echo "Unknown")

if [[ "$STATUS" == "ProviderInstalled" ]]; then
  echo "  Install succeeded on first attempt!"
else
  echo "  Status: $STATUS — triggering retry via delete/recreate of provider only..."
  # CRDs now have valid CA from cert-manager. Delete provider (keep CRDs) and recreate.
  kubectl --context "$CONTEXT" delete infrastructureprovider aws -n capa-system 2>/dev/null || true
  sleep 5
  kubectl --context "$CONTEXT" apply -f "$ROOT_DIR/platform/control-plane-manifests/capi-providers.yaml"
  echo "  Waiting 60s for second attempt..."
  sleep 60
  STATUS=$(kubectl --context "$CONTEXT" get infrastructureprovider aws -n capa-system \
    -o jsonpath='{.status.conditions[?(@.type=="ProviderInstalled")].reason}' 2>/dev/null || echo "Unknown")
fi

echo ""
if [[ "$STATUS" == "ProviderInstalled" ]]; then
  echo "=== CAPA reinstalled successfully ==="
  echo "Feature gates:"
  kubectl --context "$CONTEXT" -n capa-system get deployment capa-controller-manager \
    -o jsonpath='{.spec.template.spec.containers[0].args}' 2>&1 | tr ',' '\n' | grep -iE 'iam|machine'
  echo ""
  echo "IRSA annotation:"
  kubectl --context "$CONTEXT" -n capa-system get sa capa-controller-manager \
    -o jsonpath='{.metadata.annotations.eks\.amazonaws\.com/role-arn}' 2>&1
  echo ""
else
  echo "=== CAPA install failed: $STATUS ==="
  echo "Check: kubectl --context $CONTEXT get infrastructureprovider aws -n capa-system -o yaml"
  exit 1
fi
