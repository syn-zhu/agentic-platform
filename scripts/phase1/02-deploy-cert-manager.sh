#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

echo "=== Phase 1.2: Deploying cert-manager + Root CA ==="

CONTEXT="agentic-mgmt"

# Verify we're targeting the management cluster
echo "Verifying cluster context: $CONTEXT"
kubectl --context "$CONTEXT" cluster-info > /dev/null 2>&1 || {
  echo "ERROR: Cannot reach cluster $CONTEXT. Run 00-create-management-cluster.sh first."
  exit 1
}

# Deploy cert-manager via helmfile
echo "Installing cert-manager..."
cd "$ROOT_DIR/platform/management"
KUBECONFIG_CONTEXT="$CONTEXT" helmfile --kube-context "$CONTEXT" -l name=cert-manager sync
cd "$ROOT_DIR"

# Wait for cert-manager webhook to be ready
echo "Waiting for cert-manager webhook..."
kubectl --context "$CONTEXT" -n cert-manager rollout status deployment/cert-manager-webhook --timeout=120s

# Apply root CA manifests
echo "Creating root CA chain..."
kubectl --context "$CONTEXT" apply -f "$ROOT_DIR/platform/management-manifests/root-ca.yaml"

# Wait for root CA certificate to be ready
echo "Waiting for root CA certificate to be issued..."
kubectl --context "$CONTEXT" -n cert-manager wait certificate/agentic-platform-root-ca \
  --for=condition=Ready --timeout=60s

echo ""
echo "=== cert-manager + Root CA deployed ==="
echo "Root CA secret: cert-manager/agentic-platform-root-ca"
echo "ClusterIssuer: agentic-platform-root-ca"
echo ""
echo "To issue an intermediate CA for a cluster:"
echo "  CLUSTER_NAME=control-plane envsubst '\$CLUSTER_NAME' < platform/management-manifests/intermediate-ca-template.yaml | kubectl --context $CONTEXT apply -f -"
