#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "=== Tearing down Transit Gateway ==="

REGION="us-east-1"
TGW_NAME="agentic-platform-tgw"

TGW_ID=$(aws ec2 describe-transit-gateways \
  --filters "Name=tag:Name,Values=$TGW_NAME" "Name=state,Values=available" \
  --query 'TransitGateways[0].TransitGatewayId' --output text --region "$REGION" 2>/dev/null)

if [[ -z "$TGW_ID" || "$TGW_ID" == "None" ]]; then
  echo "No Transit Gateway found with name $TGW_NAME"
  exit 0
fi

echo "Found TGW: $TGW_ID"

# Delete all attachments first
ATTACHMENTS=$(aws ec2 describe-transit-gateway-vpc-attachments \
  --filters "Name=transit-gateway-id,Values=$TGW_ID" "Name=state,Values=available" \
  --query 'TransitGatewayVpcAttachments[].TransitGatewayAttachmentId' --output text --region "$REGION")

for ATT in $ATTACHMENTS; do
  echo "  Deleting attachment: $ATT"
  aws ec2 delete-transit-gateway-vpc-attachment --transit-gateway-attachment-id "$ATT" --region "$REGION"
done

if [[ -n "$ATTACHMENTS" ]]; then
  echo "  Waiting for attachments to be deleted..."
  sleep 30
fi

# Delete TGW
echo "Deleting Transit Gateway $TGW_ID..."
aws ec2 delete-transit-gateway --transit-gateway-id "$TGW_ID" --region "$REGION"

rm -f "$SCRIPT_DIR/.tgw-id"
echo "=== Transit Gateway deleted ==="
