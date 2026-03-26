#!/usr/bin/env bash
set -euo pipefail

# Configures kubectl access to CAPA-managed clusters by adding the current IAM user
# to each cluster's aws-auth ConfigMap. Uses CAPA-generated kubeconfigs (stored as
# secrets on the management cluster) for initial access.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

echo "=== Configuring cluster access ==="

MGMT_CTX="agentic-mgmt"
REGION="us-east-1"
CLUSTERS=("agentic-cp" "agentic-obs" "agentic-cell-1" "agentic-cell-2")

ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
USER_ARN="arn:aws:iam::${ACCOUNT_ID}:user/$(aws sts get-caller-identity --query 'Arn' --output text | awk -F/ '{print $NF}')"
echo "IAM User: $USER_ARN"

TMPDIR=$(mktemp -d)
trap "rm -rf $TMPDIR" EXIT

for CLUSTER in "${CLUSTERS[@]}"; do
  echo ""
  echo "── $CLUSTER ──"

  # Extract CAPA kubeconfig from management cluster
  KUBECONFIG_FILE="$TMPDIR/${CLUSTER}-kubeconfig"
  kubectl --context "$MGMT_CTX" -n default get secret "${CLUSTER}-kubeconfig" \
    -o jsonpath='{.data.value}' | base64 -d > "$KUBECONFIG_FILE"

  # Check if aws-auth ConfigMap exists
  if kubectl --kubeconfig "$KUBECONFIG_FILE" -n kube-system get configmap aws-auth > /dev/null 2>&1; then
    echo "  aws-auth exists, checking for user entry..."

    # Check if user is already in mapUsers
    EXISTING=$(kubectl --kubeconfig "$KUBECONFIG_FILE" -n kube-system get configmap aws-auth \
      -o jsonpath='{.data.mapUsers}' 2>/dev/null || echo "")

    if echo "$EXISTING" | grep -q "$USER_ARN"; then
      echo "  User already in aws-auth, skipping."
    else
      echo "  Adding user to aws-auth..."
      # Patch aws-auth to add the IAM user with system:masters group
      kubectl --kubeconfig "$KUBECONFIG_FILE" -n kube-system get configmap aws-auth -o json | \
        python3 -c "
import json, sys, yaml
cm = json.load(sys.stdin)
users = yaml.safe_load(cm['data'].get('mapUsers', '[]')) or []
users.append({
    'userarn': '$USER_ARN',
    'username': 'admin',
    'groups': ['system:masters']
})
cm['data']['mapUsers'] = yaml.dump(users, default_flow_style=False)
json.dump(cm, sys.stdout)
" | kubectl --kubeconfig "$KUBECONFIG_FILE" apply -f -
      echo "  User added."
    fi
  else
    echo "  aws-auth does not exist, creating..."
    kubectl --kubeconfig "$KUBECONFIG_FILE" -n kube-system apply -f - <<AUTHEOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: aws-auth
  namespace: kube-system
data:
  mapUsers: |
    - userarn: $USER_ARN
      username: admin
      groups:
        - system:masters
AUTHEOF
    echo "  aws-auth created."
  fi

  # Update kubeconfig to use standard aws eks auth
  aws eks update-kubeconfig --name "$CLUSTER" --region "$REGION" --alias "$CLUSTER" 2>&1

  # Verify access
  echo "  Verifying..."
  if kubectl --context "$CLUSTER" cluster-info > /dev/null 2>&1; then
    echo "  ✓ kubectl access working"
  else
    echo "  ✗ kubectl access still failing"
  fi
done

echo ""
echo "=== Cluster access configured ==="
