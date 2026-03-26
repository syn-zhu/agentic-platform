#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

echo "=== Phase 1.3: Deploying Cluster API + CAPA ==="

CONTEXT="agentic-mgmt"
REGION="us-east-1"

# Verify cluster
kubectl --context "$CONTEXT" cluster-info > /dev/null 2>&1 || {
  echo "ERROR: Cannot reach cluster $CONTEXT."
  exit 1
}

# Verify cert-manager is running (CAPA depends on it)
kubectl --context "$CONTEXT" -n cert-manager get deployment cert-manager-webhook > /dev/null 2>&1 || {
  echo "ERROR: cert-manager not found. Run 02-deploy-cert-manager.sh first."
  exit 1
}

# Set up AWS credentials for CAPA
# CAPA needs AWS credentials to provision EKS clusters
ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
echo "AWS Account ID: $ACCOUNT_ID"

# Create CAPA IAM role and IRSA binding
echo "Setting up CAPA IRSA..."
OIDC_PROVIDER=$(aws eks describe-cluster --name "agentic-mgmt" --region "$REGION" \
  --query 'cluster.identity.oidc.issuer' --output text | sed 's|https://||')

CAPA_ROLE_NAME="agentic-mgmt-capa-controller"
CAPA_POLICY_ARN="arn:aws:iam::${ACCOUNT_ID}:policy/capa-controller"

# Create trust policy for IRSA
cat > /tmp/capa-trust-policy.json <<TRUST
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {
        "Federated": "arn:aws:iam::${ACCOUNT_ID}:oidc-provider/${OIDC_PROVIDER}"
      },
      "Action": "sts:AssumeRoleWithWebIdentity",
      "Condition": {
        "StringEquals": {
          "${OIDC_PROVIDER}:sub": "system:serviceaccount:capa-system:capa-controller-manager",
          "${OIDC_PROVIDER}:aud": "sts.amazonaws.com"
        }
      }
    }
  ]
}
TRUST

aws iam create-role \
  --role-name "$CAPA_ROLE_NAME" \
  --assume-role-policy-document "file:///tmp/capa-trust-policy.json" \
  --tags "Key=project,Value=agentic-platform" \
  2>/dev/null || echo "  CAPA IAM role already exists."

aws iam attach-role-policy \
  --role-name "$CAPA_ROLE_NAME" \
  --policy-arn "$CAPA_POLICY_ARN" \
  2>/dev/null || echo "  Policy already attached."

CAPA_ROLE_ARN="arn:aws:iam::${ACCOUNT_ID}:role/${CAPA_ROLE_NAME}"

# Deploy CAPI Operator via helmfile
echo "Installing Cluster API Operator..."
cd "$ROOT_DIR/platform/management"
KUBECONFIG_CONTEXT="$CONTEXT" helmfile --kube-context "$CONTEXT" sync
cd "$ROOT_DIR"

# Create capa-system namespace (operator creates it on reconcile, but CR needs it to exist)
kubectl --context "$CONTEXT" create namespace capa-system --dry-run=client -o yaml \
  | kubectl --context "$CONTEXT" apply -f -

# Create the AWS infrastructure provider with IRSA annotation
# No configSecret needed — IRSA provides credentials via the SA annotation
echo "Creating CAPA infrastructure provider..."
kubectl --context "$CONTEXT" apply -f - <<PROVIDER
apiVersion: operator.cluster.x-k8s.io/v1alpha2
kind: InfrastructureProvider
metadata:
  name: aws
  namespace: capa-system
spec:
  version: v2.7.1
  manager:
    serviceAccountAnnotations:
      eks.amazonaws.com/role-arn: ${CAPA_ROLE_ARN}
PROVIDER

echo "Waiting for CAPA controller to be ready..."
sleep 10
kubectl --context "$CONTEXT" -n capa-system wait deployment --all \
  --for=condition=Available --timeout=180s 2>/dev/null || {
  echo "  CAPA controller still starting, this is normal on first install."
  echo "  Check status with: kubectl --context $CONTEXT -n capa-system get pods"
}

echo ""
echo "=== Cluster API + CAPA deployed ==="
echo "CAPA IAM Role: $CAPA_ROLE_ARN"
echo "Verify: kubectl --context $CONTEXT -n capa-system get pods"
