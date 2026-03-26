#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

echo "=== Phase 1.1: Transit Gateway Setup ==="

# Load environment variables
if [[ -f "$ROOT_DIR/.env" ]]; then
  set -a; source "$ROOT_DIR/.env"; set +a
fi

# Create TGW and attach management VPC
"$ROOT_DIR/cluster/transit-gateway/create-tgw.sh"

# Add VPC route for TGW
echo ""
echo "Adding VPC route table entries for TGW..."

REGION="us-east-1"
TGW_ID=$(cat "$ROOT_DIR/cluster/transit-gateway/.tgw-id")
MGMT_VPC_ID=$(aws eks describe-cluster --name "agentic-mgmt" --region "$REGION" \
  --query 'cluster.resourcesVpcConfig.vpcId' --output text)

# Get all route tables for the VPC and add routes for other cluster CIDRs
ROUTE_TABLES=$(aws ec2 describe-route-tables \
  --filters "Name=vpc-id,Values=$MGMT_VPC_ID" \
  --query 'RouteTables[].RouteTableId' --output text --region "$REGION")

# CIDRs of future clusters (control-plane, gateway, obs, cell-1, cell-2)
REMOTE_CIDRS=("10.1.0.0/16" "10.2.0.0/16" "10.3.0.0/16" "10.4.0.0/16" "10.5.0.0/16")

for RT in $ROUTE_TABLES; do
  for CIDR in "${REMOTE_CIDRS[@]}"; do
    aws ec2 create-route \
      --route-table-id "$RT" \
      --destination-cidr-block "$CIDR" \
      --transit-gateway-id "$TGW_ID" \
      --region "$REGION" 2>/dev/null || true
  done
done

echo "=== Transit Gateway setup complete ==="
