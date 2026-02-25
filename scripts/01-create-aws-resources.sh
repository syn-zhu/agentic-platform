#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

echo "=== Phase 1: Creating AWS Managed Resources ==="

# Load environment variables
if [[ -f "$ROOT_DIR/.env" ]]; then
  set -a; source "$ROOT_DIR/.env"; set +a
fi

# ── Configuration ──
REGION="us-east-1"
CLUSTER_NAME="agentic-platform"
ENV_NAME="${ENV_NAME:-dev}"
DB_INSTANCE_CLASS="${DB_INSTANCE_CLASS:-db.t4g.small}"
CACHE_NODE_TYPE="${CACHE_NODE_TYPE:-cache.t4g.small}"
S3_BUCKET="agentic-platform-langfuse-${ENV_NAME}"

# Generate secure passwords
DB_PASSWORD=$(openssl rand -base64 24 | tr -d '/+=' | head -c 32)
KEYCLOAK_DB_PASSWORD=$(openssl rand -base64 24 | tr -d '/+=' | head -c 32)
REDIS_PASSWORD=$(openssl rand -base64 24 | tr -d '/+=' | head -c 32)

# ── Discover VPC and subnet info from EKS cluster ──
echo "Discovering VPC configuration from EKS cluster..."
VPC_ID=$(aws eks describe-cluster --name "$CLUSTER_NAME" --region "$REGION" \
  --query 'cluster.resourcesVpcConfig.vpcId' --output text)
echo "  VPC ID: $VPC_ID"

PRIVATE_SUBNETS=$(aws ec2 describe-subnets \
  --filters "Name=vpc-id,Values=$VPC_ID" "Name=tag:aws:cloudformation:logical-id,Values=SubnetPrivate*" \
  --query 'Subnets[].SubnetId' --output text --region "$REGION")
SUBNET_IDS=$(echo "$PRIVATE_SUBNETS" | tr '\t' ',')
echo "  Private subnets: $SUBNET_IDS"

# Create a security group for RDS/ElastiCache
echo "Creating security group for managed services..."
SG_ID=$(aws ec2 create-security-group \
  --group-name "agentic-platform-managed-services" \
  --description "Security group for RDS and ElastiCache used by agentic platform" \
  --vpc-id "$VPC_ID" \
  --region "$REGION" \
  --query 'GroupId' --output text 2>/dev/null || \
  aws ec2 describe-security-groups \
    --filters "Name=group-name,Values=agentic-platform-managed-services" "Name=vpc-id,Values=$VPC_ID" \
    --query 'SecurityGroups[0].GroupId' --output text --region "$REGION")
echo "  Security Group: $SG_ID"

# Allow inbound from VPC CIDR
VPC_CIDR=$(aws ec2 describe-vpcs --vpc-ids "$VPC_ID" --query 'Vpcs[0].CidrBlock' --output text --region "$REGION")
aws ec2 authorize-security-group-ingress \
  --group-id "$SG_ID" \
  --protocol tcp --port 5432 --cidr "$VPC_CIDR" \
  --region "$REGION" 2>/dev/null || true
aws ec2 authorize-security-group-ingress \
  --group-id "$SG_ID" \
  --protocol tcp --port 6379 --cidr "$VPC_CIDR" \
  --region "$REGION" 2>/dev/null || true

# ── 1. Create RDS PostgreSQL ──
echo ""
echo "Creating RDS PostgreSQL instance..."
DB_SUBNET_GROUP="agentic-platform-db-subnet"
aws rds create-db-subnet-group \
  --db-subnet-group-name "$DB_SUBNET_GROUP" \
  --db-subnet-group-description "Subnet group for agentic platform RDS" \
  --subnet-ids $PRIVATE_SUBNETS \
  --region "$REGION" 2>/dev/null || echo "  DB subnet group already exists."

aws rds create-db-instance \
  --db-instance-identifier "agentic-platform-langfuse" \
  --db-instance-class "$DB_INSTANCE_CLASS" \
  --engine postgres \
  --engine-version "17" \
  --master-username "langfuse" \
  --master-user-password "$DB_PASSWORD" \
  --allocated-storage 20 \
  --storage-type gp3 \
  --storage-encrypted \
  --vpc-security-group-ids "$SG_ID" \
  --db-subnet-group-name "$DB_SUBNET_GROUP" \
  --db-name "langfuse" \
  --no-multi-az \
  --backup-retention-period 1 \
  --region "$REGION" \
  --tags "Key=project,Value=agentic-platform" 2>/dev/null || echo "  RDS instance already exists."

echo "  Waiting for RDS to become available (this may take several minutes)..."
aws rds wait db-instance-available --db-instance-identifier "agentic-platform-langfuse" --region "$REGION"

RDS_ENDPOINT=$(aws rds describe-db-instances \
  --db-instance-identifier "agentic-platform-langfuse" \
  --query 'DBInstances[0].Endpoint.Address' --output text --region "$REGION")
echo "  RDS Endpoint: $RDS_ENDPOINT"

# ── 2. Create ElastiCache Redis ──
echo ""
echo "Creating ElastiCache Redis cluster..."
CACHE_SUBNET_GROUP="agentic-platform-cache-subnet"
aws elasticache create-cache-subnet-group \
  --cache-subnet-group-name "$CACHE_SUBNET_GROUP" \
  --cache-subnet-group-description "Subnet group for agentic platform ElastiCache" \
  --subnet-ids $PRIVATE_SUBNETS \
  --region "$REGION" 2>/dev/null || echo "  Cache subnet group already exists."

aws elasticache create-replication-group \
  --replication-group-id "agentic-platform-redis" \
  --replication-group-description "Redis for Langfuse" \
  --engine redis \
  --cache-node-type "$CACHE_NODE_TYPE" \
  --num-cache-clusters 1 \
  --transit-encryption-enabled \
  --auth-token "$REDIS_PASSWORD" \
  --cache-subnet-group-name "$CACHE_SUBNET_GROUP" \
  --security-group-ids "$SG_ID" \
  --region "$REGION" \
  --tags "Key=project,Value=agentic-platform" 2>/dev/null || echo "  Redis replication group already exists."

echo "  Waiting for Redis to become available..."
aws elasticache wait replication-group-available \
  --replication-group-id "agentic-platform-redis" --region "$REGION"

REDIS_ENDPOINT=$(aws elasticache describe-replication-groups \
  --replication-group-id "agentic-platform-redis" \
  --query 'ReplicationGroups[0].NodeGroups[0].PrimaryEndpoint.Address' --output text --region "$REGION")
echo "  Redis Endpoint: $REDIS_ENDPOINT"

# ── 3. Create S3 Bucket ──
echo ""
echo "Creating S3 bucket for Langfuse..."
aws s3 mb "s3://$S3_BUCKET" --region "$REGION" 2>/dev/null || echo "  S3 bucket already exists."

aws s3api put-bucket-encryption \
  --bucket "$S3_BUCKET" \
  --server-side-encryption-configuration '{
    "Rules": [{"ApplyServerSideEncryptionByDefault": {"SSEAlgorithm": "AES256"}}]
  }' --region "$REGION"

aws s3api put-public-access-block \
  --bucket "$S3_BUCKET" \
  --public-access-block-configuration \
    'BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true' \
  --region "$REGION"

# ── 4. Create Keycloak database and dedicated user on the same RDS instance ──
echo ""
echo "Creating keycloak database on RDS via in-cluster psql pod..."
kubectl run psql-init --rm -i --restart=Never \
  --image=postgres:17-alpine \
  --env="PGPASSWORD=${DB_PASSWORD}" \
  -- psql "postgresql://langfuse:${DB_PASSWORD}@${RDS_ENDPOINT}:5432/langfuse" \
  -c "CREATE DATABASE keycloak;" \
  2>/dev/null || echo "  keycloak database may already exist."

echo "Creating dedicated keycloak database user..."
kubectl run psql-keycloak-user --rm -i --restart=Never \
  --image=postgres:17-alpine \
  --env="PGPASSWORD=${DB_PASSWORD}" \
  -- psql "postgresql://langfuse:${DB_PASSWORD}@${RDS_ENDPOINT}:5432/langfuse" \
  -c "CREATE USER keycloak WITH PASSWORD '${KEYCLOAK_DB_PASSWORD}';" \
  -c "GRANT ALL PRIVILEGES ON DATABASE keycloak TO keycloak;" \
  -c "ALTER DATABASE keycloak OWNER TO keycloak;" \
  2>/dev/null || echo "  keycloak user may already exist."

kubectl run psql-keycloak-schema --rm -i --restart=Never \
  --image=postgres:17-alpine \
  --env="PGPASSWORD=${DB_PASSWORD}" \
  -- psql "postgresql://langfuse:${DB_PASSWORD}@${RDS_ENDPOINT}:5432/keycloak" \
  -c "GRANT ALL ON SCHEMA public TO keycloak;" \
  -c "ALTER SCHEMA public OWNER TO keycloak;" \
  2>/dev/null || echo "  keycloak schema grants may already exist."
echo "  keycloak database and user ready."

# ── 5. Create AgentRegistry database and dedicated user ──
echo ""
echo "Creating agentregistry database on RDS via in-cluster psql pod..."
AGENTREGISTRY_DB_PASSWORD=$(openssl rand -base64 24 | tr -d '/+=' | head -c 32)

kubectl run psql-agentregistry-db --rm -i --restart=Never \
  --image=postgres:17-alpine \
  --env="PGPASSWORD=${DB_PASSWORD}" \
  -- psql "postgresql://langfuse:${DB_PASSWORD}@${RDS_ENDPOINT}:5432/langfuse" \
  -c "CREATE DATABASE agentregistry;" \
  2>/dev/null || echo "  agentregistry database may already exist."

echo "Creating dedicated agentregistry database user..."
kubectl run psql-agentregistry-user --rm -i --restart=Never \
  --image=postgres:17-alpine \
  --env="PGPASSWORD=${DB_PASSWORD}" \
  -- psql "postgresql://langfuse:${DB_PASSWORD}@${RDS_ENDPOINT}:5432/langfuse" \
  -c "CREATE USER agentregistry WITH PASSWORD '${AGENTREGISTRY_DB_PASSWORD}';" \
  -c "GRANT ALL PRIVILEGES ON DATABASE agentregistry TO agentregistry;" \
  -c "ALTER DATABASE agentregistry OWNER TO agentregistry;" \
  2>/dev/null || echo "  agentregistry user may already exist."

echo "Enabling pgvector extension..."
kubectl run psql-agentregistry-ext --rm -i --restart=Never \
  --image=postgres:17-alpine \
  --env="PGPASSWORD=${DB_PASSWORD}" \
  -- psql "postgresql://langfuse:${DB_PASSWORD}@${RDS_ENDPOINT}:5432/agentregistry" \
  -c "CREATE EXTENSION IF NOT EXISTS vector;" \
  2>/dev/null || echo "  pgvector extension may already exist."

kubectl run psql-agentregistry-schema --rm -i --restart=Never \
  --image=postgres:17-alpine \
  --env="PGPASSWORD=${DB_PASSWORD}" \
  -- psql "postgresql://langfuse:${DB_PASSWORD}@${RDS_ENDPOINT}:5432/agentregistry" \
  -c "GRANT ALL ON SCHEMA public TO agentregistry;" \
  -c "ALTER SCHEMA public OWNER TO agentregistry;" \
  2>/dev/null || echo "  agentregistry schema grants may already exist."
echo "  agentregistry database and user ready."

# ── Save outputs for secret creation ──
echo ""
echo "=== AWS Resources Created ==="
OUTPUTS_FILE="$ROOT_DIR/.aws-outputs.env"
cat > "$OUTPUTS_FILE" <<EOF
# Generated by 01-create-aws-resources.sh — DO NOT COMMIT
RDS_ENDPOINT=$RDS_ENDPOINT
RDS_PASSWORD=$DB_PASSWORD
RDS_DATABASE_URL=postgresql://langfuse:${DB_PASSWORD}@${RDS_ENDPOINT}:5432/langfuse
KEYCLOAK_DB_PASSWORD=$KEYCLOAK_DB_PASSWORD
REDIS_ENDPOINT=$REDIS_ENDPOINT
REDIS_PASSWORD=$REDIS_PASSWORD
S3_BUCKET=$S3_BUCKET
S3_REGION=$REGION
AGENTREGISTRY_DB_PASSWORD=$AGENTREGISTRY_DB_PASSWORD
EOF

echo "Outputs saved to $OUTPUTS_FILE"
echo ""
echo "Next step: ./02-create-secrets.sh"
