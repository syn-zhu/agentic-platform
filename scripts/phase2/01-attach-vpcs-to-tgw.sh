#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

echo "=== Phase 2.1: Attaching VPCs to Transit Gateway ==="

REGION="us-east-1"
TGW_ID=$(cat "$ROOT_DIR/cluster/transit-gateway/.tgw-id")
EKS_NAMES=("agentic-cp" "agentic-obs" "agentic-cell-1" "agentic-cell-2")
ALL_CIDRS=("10.0.0.0/16" "10.1.0.0/16" "10.2.0.0/16" "10.3.0.0/16" "10.4.0.0/16")

for EKS_NAME in "${EKS_NAMES[@]}"; do
  echo ""
  echo "── Attaching: $EKS_NAME ──"
  VPC_ID=$(aws eks describe-cluster --name "$EKS_NAME" --region "$REGION" \
    --query 'cluster.resourcesVpcConfig.vpcId' --output text 2>/dev/null || echo "")
  [[ -z "$VPC_ID" || "$VPC_ID" == "None" ]] && echo "  Skipping — not found." && continue

  # Get private subnets (array to handle tab-separated output)
  SUBNETS=($(aws ec2 describe-subnets \
    --filters "Name=vpc-id,Values=$VPC_ID" "Name=map-public-ip-on-launch,Values=false" \
    --query 'Subnets[].SubnetId' --output text --region "$REGION"))
  echo "  VPC: $VPC_ID, Subnets: ${SUBNETS[*]}"

  # Create TGW attachment
  aws ec2 create-transit-gateway-vpc-attachment \
    --transit-gateway-id "$TGW_ID" --vpc-id "$VPC_ID" \
    --subnet-ids "${SUBNETS[@]}" \
    --tag-specifications "ResourceType=transit-gateway-attachment,Tags=[{Key=Name,Value=$EKS_NAME},{Key=project,Value=agentic-platform}]" \
    --region "$REGION" 2>/dev/null || echo "  Attachment already exists."

  # Add routes to all other CIDRs
  VPC_CIDR=$(aws ec2 describe-vpcs --vpc-ids "$VPC_ID" \
    --query 'Vpcs[0].CidrBlock' --output text --region "$REGION")
  ROUTE_TABLES=($(aws ec2 describe-route-tables \
    --filters "Name=vpc-id,Values=$VPC_ID" \
    --query 'RouteTables[].RouteTableId' --output text --region "$REGION"))

  for RT in "${ROUTE_TABLES[@]}"; do
    for CIDR in "${ALL_CIDRS[@]}"; do
      [[ "$CIDR" == "$VPC_CIDR" ]] && continue
      aws ec2 create-route --route-table-id "$RT" \
        --destination-cidr-block "$CIDR" --transit-gateway-id "$TGW_ID" \
        --region "$REGION" 2>/dev/null || true
    done
  done

  # Add SG rule for HTTPS (443) from all VPCs
  SG_ID=$(aws eks describe-cluster --name "$EKS_NAME" --region "$REGION" \
    --query 'cluster.resourcesVpcConfig.clusterSecurityGroupId' --output text)
  for CIDR in "${ALL_CIDRS[@]}"; do
    aws ec2 authorize-security-group-ingress \
      --group-id "$SG_ID" --protocol tcp --port 443 --cidr "$CIDR" \
      --region "$REGION" 2>/dev/null || true
  done
  echo "  Routes + SG rules added."
done

echo ""
echo "=== VPC attachments complete ==="
