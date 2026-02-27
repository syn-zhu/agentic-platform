#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

echo "=== Phase 0: Creating EKS Cluster ==="

# Load environment variables
if [[ -f "$ROOT_DIR/.env" ]]; then
  set -a; source "$ROOT_DIR/.env"; set +a
fi

# Resolve AWS Account ID
ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
echo "AWS Account ID: $ACCOUNT_ID"

# Substitute ACCOUNT_ID into cluster.yaml
CLUSTER_CONFIG=$(mktemp)
sed "s/\${ACCOUNT_ID}/$ACCOUNT_ID/g" "$ROOT_DIR/cluster/cluster.yaml" > "$CLUSTER_CONFIG"

# Create the IAM policy for Langfuse S3 access (idempotent)
echo "Creating IAM policy langfuse-s3-access..."
aws iam create-policy \
  --policy-name langfuse-s3-access \
  --policy-document "file://$ROOT_DIR/cluster/iam-policies/langfuse-s3-policy.json" \
  2>/dev/null || echo "  Policy langfuse-s3-access already exists, skipping."

# Create the IAM policy for Cluster Autoscaler (idempotent)
echo "Creating IAM policy cluster-autoscaler..."
aws iam create-policy \
  --policy-name cluster-autoscaler \
  --policy-document "file://$ROOT_DIR/cluster/iam-policies/cluster-autoscaler-policy.json" \
  2>/dev/null || echo "  Policy cluster-autoscaler already exists, skipping."

# Create EKS cluster
echo "Creating EKS cluster (this will take ~15-20 minutes)..."
eksctl create cluster -f "$CLUSTER_CONFIG"

# Update kubeconfig
echo "Updating kubeconfig..."
aws eks update-kubeconfig --name agentic-platform --region us-east-1

# ── Create gp3 StorageClass ──
# EKS ships with gp2 only; our Helm charts reference gp3 for better performance.
echo "Creating gp3 StorageClass..."
kubectl apply -f - <<'SCEOF'
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
SCEOF

echo "=== Cluster creation complete ==="
echo "Verify with: kubectl get nodes"
