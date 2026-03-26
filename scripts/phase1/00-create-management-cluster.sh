#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

echo "=== Phase 1.0: Creating Management Cluster ==="

# Load environment variables
if [[ -f "$ROOT_DIR/.env" ]]; then
  set -a; source "$ROOT_DIR/.env"; set +a
fi

REGION="us-east-1"
CLUSTER_NAME="agentic-mgmt"

# Resolve AWS Account ID
ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
echo "AWS Account ID: $ACCOUNT_ID"

# Create CAPA controller IAM policy (idempotent)
echo "Creating CAPA controller IAM policy..."
CAPA_POLICY_ARN="arn:aws:iam::${ACCOUNT_ID}:policy/capa-controller"
aws iam create-policy \
  --policy-name capa-controller \
  --policy-document "file://$ROOT_DIR/cluster/management/iam-policies/capa-controller-policy.json" \
  2>/dev/null || echo "  Policy capa-controller already exists, skipping."

# Create EKS cluster
echo "Creating management EKS cluster (this will take ~15-20 minutes)..."
eksctl create cluster -f "$ROOT_DIR/cluster/management/cluster.yaml"

# Update kubeconfig with a unique context name
echo "Updating kubeconfig..."
aws eks update-kubeconfig \
  --name "$CLUSTER_NAME" \
  --region "$REGION" \
  --alias "$CLUSTER_NAME"

# Create gp3 StorageClass
echo "Creating gp3 StorageClass..."
kubectl --context "$CLUSTER_NAME" apply -f - <<'EOF'
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: gp3
  annotations:
    storageclass.kubernetes.io/is-default-class: "true"
provisioner: ebs.csi.aws.com
parameters:
  type: gp3
  encrypted: "true"
reclaimPolicy: Delete
volumeBindingMode: WaitForFirstConsumer
allowVolumeExpansion: true
EOF

echo "=== Management cluster created ==="
echo "Context: $CLUSTER_NAME"
echo "Verify: kubectl --context $CLUSTER_NAME get nodes"
