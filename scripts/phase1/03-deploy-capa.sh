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

# ── 1. Set up CAPA IAM role with IRSA ──
ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
echo "AWS Account ID: $ACCOUNT_ID"

echo "Setting up CAPA IRSA..."
OIDC_PROVIDER=$(aws eks describe-cluster --name "agentic-mgmt" --region "$REGION" \
  --query 'cluster.identity.oidc.issuer' --output text | sed 's|https://||')

CAPA_ROLE_NAME="agentic-mgmt-capa-controller"
CAPA_POLICY_ARN="arn:aws:iam::${ACCOUNT_ID}:policy/capa-controller"

# Create trust policy for IRSA (written to temp file for aws cli)
TRUST_POLICY=$(mktemp)
cat > "$TRUST_POLICY" <<TRUST
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
  --assume-role-policy-document "file://$TRUST_POLICY" \
  --tags "Key=project,Value=agentic-platform" \
  2>/dev/null || echo "  CAPA IAM role already exists."
rm -f "$TRUST_POLICY"

aws iam attach-role-policy \
  --role-name "$CAPA_ROLE_NAME" \
  --policy-arn "$CAPA_POLICY_ARN" \
  2>/dev/null || echo "  Policy already attached."

CAPA_ROLE_ARN="arn:aws:iam::${ACCOUNT_ID}:role/${CAPA_ROLE_NAME}"

# ── 2. Deploy CAPI Operator via helmfile ──
echo "Installing Cluster API Operator..."
cd "$ROOT_DIR/platform/management"
KUBECONFIG_CONTEXT="$CONTEXT" helmfile --kube-context "$CONTEXT" sync
cd "$ROOT_DIR"

# ── 3. Create namespaces and AWS credentials secret ──
# CAPA requires an AWS credentials secret even when using IRSA.
# With IRSA, the actual credentials come from the pod's projected SA token,
# but CAPA's variable substitution still requires the secret to exist.
echo "Creating CAPA prerequisites..."
kubectl --context "$CONTEXT" create namespace capa-system --dry-run=client -o yaml \
  | kubectl --context "$CONTEXT" apply -f -

kubectl --context "$CONTEXT" -n capa-system create secret generic aws-credentials \
  --from-literal=AWS_B64ENCODED_CREDENTIALS="$(printf '[default]\naws_access_key_id = \naws_secret_access_key = \n' | base64)" \
  --dry-run=client -o yaml | kubectl --context "$CONTEXT" apply -f -

# ── 4. Apply CAPI provider manifests from source of truth ──
echo "Creating CAPI Core + Infrastructure providers..."
kubectl --context "$CONTEXT" apply -f "$ROOT_DIR/platform/management-manifests/capi-providers.yaml"

# ── 5. Wait for core provider, then annotate CAPA SA with IRSA ──
echo "Waiting for CAPI core controller..."
sleep 15
kubectl --context "$CONTEXT" -n capi-system wait deployment --all \
  --for=condition=Available --timeout=180s 2>/dev/null || true

echo "Waiting for CAPA controller..."
sleep 30
kubectl --context "$CONTEXT" -n capa-system wait deployment --all \
  --for=condition=Available --timeout=180s 2>/dev/null || {
  echo "  CAPA controller still starting. Check: kubectl --context $CONTEXT -n capa-system get pods"
}

# Annotate CAPA service account with IRSA role
# This must happen after the operator creates the SA
if kubectl --context "$CONTEXT" -n capa-system get serviceaccount capa-controller-manager > /dev/null 2>&1; then
  echo "Annotating CAPA SA with IRSA role..."
  kubectl --context "$CONTEXT" -n capa-system annotate serviceaccount capa-controller-manager \
    "eks.amazonaws.com/role-arn=$CAPA_ROLE_ARN" --overwrite
  # Restart to pick up IRSA annotation
  kubectl --context "$CONTEXT" -n capa-system rollout restart deployment
  kubectl --context "$CONTEXT" -n capa-system wait deployment --all \
    --for=condition=Available --timeout=120s 2>/dev/null || true
else
  echo "  WARNING: CAPA SA not yet created. Annotate manually later:"
  echo "    kubectl --context $CONTEXT -n capa-system annotate sa capa-controller-manager eks.amazonaws.com/role-arn=$CAPA_ROLE_ARN"
fi

echo ""
echo "=== Cluster API + CAPA deployed ==="
echo "CAPA IAM Role: $CAPA_ROLE_ARN"
echo "Verify: kubectl --context $CONTEXT -n capa-system get pods"
