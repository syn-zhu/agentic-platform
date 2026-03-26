#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "=== Creating Transit Gateway ==="

REGION="us-east-1"
TGW_NAME="agentic-platform-tgw"

# Create Transit Gateway
echo "Creating Transit Gateway..."
TGW_ID=$(aws ec2 create-transit-gateway \
  --description "Agentic Platform multi-cluster connectivity" \
  --options "AmazonSideAsn=64512,AutoAcceptSharedAttachments=enable,DefaultRouteTableAssociation=enable,DefaultRouteTablePropagation=enable,DnsSupport=enable" \
  --tag-specifications "ResourceType=transit-gateway,Tags=[{Key=Name,Value=$TGW_NAME},{Key=project,Value=agentic-platform}]" \
  --region "$REGION" \
  --query 'TransitGateway.TransitGatewayId' --output text 2>/dev/null)

if [[ -z "$TGW_ID" || "$TGW_ID" == "None" ]]; then
  echo "  TGW may already exist, looking up..."
  TGW_ID=$(aws ec2 describe-transit-gateways \
    --filters "Name=tag:Name,Values=$TGW_NAME" "Name=state,Values=available,pending" \
    --query 'TransitGateways[0].TransitGatewayId' --output text --region "$REGION")
fi

echo "  Transit Gateway ID: $TGW_ID"

# Wait for TGW to be available
echo "  Waiting for TGW to become available..."
aws ec2 wait transit-gateway-available --transit-gateway-ids "$TGW_ID" --region "$REGION" 2>/dev/null || true

# Attach management cluster VPC
echo "Attaching management cluster VPC..."
MGMT_VPC_ID=$(aws eks describe-cluster --name "agentic-mgmt" --region "$REGION" \
  --query 'cluster.resourcesVpcConfig.vpcId' --output text 2>/dev/null || echo "")

if [[ -n "$MGMT_VPC_ID" && "$MGMT_VPC_ID" != "None" ]]; then
  MGMT_SUBNETS=$(aws ec2 describe-subnets \
    --filters "Name=vpc-id,Values=$MGMT_VPC_ID" "Name=tag:aws:cloudformation:logical-id,Values=SubnetPrivate*" \
    --query 'Subnets[].SubnetId' --output text --region "$REGION")

  aws ec2 create-transit-gateway-vpc-attachment \
    --transit-gateway-id "$TGW_ID" \
    --vpc-id "$MGMT_VPC_ID" \
    --subnet-ids $MGMT_SUBNETS \
    --tag-specifications "ResourceType=transit-gateway-attachment,Tags=[{Key=Name,Value=agentic-mgmt},{Key=project,Value=agentic-platform}]" \
    --region "$REGION" 2>/dev/null || echo "  Management VPC attachment already exists."
  echo "  Management VPC ($MGMT_VPC_ID) attached."
else
  echo "  WARNING: Management cluster not found, skipping VPC attachment."
  echo "  Run 00-create-management-cluster.sh first, then re-run this script."
fi

# Save TGW ID for other scripts
echo "$TGW_ID" > "$SCRIPT_DIR/.tgw-id"
echo ""
echo "=== Transit Gateway ready ==="
echo "TGW ID: $TGW_ID"
echo "Saved to: $SCRIPT_DIR/.tgw-id"
echo ""
echo "To attach additional cluster VPCs later, run:"
echo "  aws ec2 create-transit-gateway-vpc-attachment \\"
echo "    --transit-gateway-id $TGW_ID \\"
echo "    --vpc-id <VPC_ID> --subnet-ids <SUBNET_IDS>"
