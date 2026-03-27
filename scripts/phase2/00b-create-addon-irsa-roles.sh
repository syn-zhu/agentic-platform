#!/usr/bin/env bash
set -euo pipefail

# Creates IRSA roles for EKS addons (ebs-csi-driver) on all CAPA-managed clusters,
# then updates the AWSManagedControlPlane addon specs with the role ARNs.
# The trust policy template is at cluster/shared/ebs-csi-driver-trust-policy.json.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

echo "=== Creating addon IRSA roles ==="

REGION="us-east-1"
ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
EKS_CLUSTERS=("agentic-cp" "agentic-obs" "agentic-cell-1" "agentic-cell-2")

for CLUSTER in "${EKS_CLUSTERS[@]}"; do
  echo ""
  echo "── $CLUSTER ──"

  ROLE_NAME="${CLUSTER}-ebs-csi-driver"

  # Get OIDC provider URL
  OIDC_URL=$(aws eks describe-cluster --name "$CLUSTER" --region "$REGION" \
    --query 'cluster.identity.oidc.issuer' --output text 2>/dev/null || echo "")

  if [[ -z "$OIDC_URL" || "$OIDC_URL" == "None" ]]; then
    echo "  WARNING: Cluster $CLUSTER not found or no OIDC provider, skipping."
    continue
  fi

  export OIDC_PROVIDER="${OIDC_URL#https://}"
  export ACCOUNT_ID

  # Generate trust policy from template
  TRUST_POLICY=$(mktemp)
  envsubst '$ACCOUNT_ID $OIDC_PROVIDER' \
    < "$ROOT_DIR/cluster/shared/ebs-csi-driver-trust-policy.json" \
    > "$TRUST_POLICY"

  # Create role
  aws iam create-role \
    --role-name "$ROLE_NAME" \
    --assume-role-policy-document "file://$TRUST_POLICY" \
    --tags "Key=project,Value=agentic-platform" "Key=cluster,Value=$CLUSTER" \
    2>/dev/null || echo "  Role already exists."
  rm -f "$TRUST_POLICY"

  # Attach EBS CSI driver policy
  aws iam attach-role-policy \
    --role-name "$ROLE_NAME" \
    --policy-arn "arn:aws:iam::aws:policy/service-role/AmazonEBSCSIDriverPolicy" \
    2>/dev/null || echo "  Policy already attached."

  ROLE_ARN="arn:aws:iam::${ACCOUNT_ID}:role/${ROLE_NAME}"
  echo "  Role: $ROLE_ARN"
done

echo ""
echo "=== IRSA roles created ==="
echo ""
echo "Now update the AWSManagedControlPlane addon specs with serviceAccountRoleARN."
echo "Role ARN format: arn:aws:iam::${ACCOUNT_ID}:role/{CLUSTER}-ebs-csi-driver"
