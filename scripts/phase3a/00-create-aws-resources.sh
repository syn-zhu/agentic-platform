#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

echo "=== Phase 3a: Creating AWS Managed Resources for Control-Plane ==="

# Load environment variables if present
if [[ -f "$ROOT_DIR/.env.cp" ]]; then
  set -a; source "$ROOT_DIR/.env.cp"; set +a
fi

# ── Configuration ──
REGION="${AWS_REGION:-us-east-1}"
CLUSTER_NAME="agentic-cp"
CONFIG_DIR="$ROOT_DIR/cluster/control-plane/aws-resources"

# Read config from committed JSON files (declarative source of truth)
RDS_CONFIG="$CONFIG_DIR/rds-config.json"
CACHE_CONFIG="$CONFIG_DIR/elasticache-config.json"

DB_INSTANCE_ID=$(jq -r '.instanceIdentifier' "$RDS_CONFIG")
DB_INSTANCE_CLASS=$(jq -r '.instanceClass' "$RDS_CONFIG")
DB_ENGINE_VERSION=$(jq -r '.engineVersion' "$RDS_CONFIG")
DB_ALLOCATED_STORAGE=$(jq -r '.allocatedStorage' "$RDS_CONFIG")
DB_STORAGE_TYPE=$(jq -r '.storageType' "$RDS_CONFIG")
DB_MASTER_USERNAME=$(jq -r '.masterUsername' "$RDS_CONFIG")
DB_BACKUP_RETENTION=$(jq -r '.backupRetentionPeriod' "$RDS_CONFIG")
DB_MULTI_AZ=$(jq -r '.multiAZ' "$RDS_CONFIG")

REDIS_GROUP_ID=$(jq -r '.replicationGroupId' "$CACHE_CONFIG")
REDIS_NODE_TYPE=$(jq -r '.nodeType' "$CACHE_CONFIG")
REDIS_NUM_CLUSTERS=$(jq -r '.numCacheClusters' "$CACHE_CONFIG")

S3_BUCKET="agentic-platform-langfuse-cp"

# DB subnet group / cache subnet group names
DB_SUBNET_GROUP="agentic-cp-db-subnet"
CACHE_SUBNET_GROUP="agentic-cp-cache-subnet"
SG_NAME="agentic-cp-managed-services"

# Generate secure passwords (only if not already set from .env.cp)
DB_PASSWORD="${DB_PASSWORD:-$(openssl rand -base64 24 | tr -d '/+=' | head -c 32)}"
REDIS_PASSWORD="${REDIS_PASSWORD:-$(openssl rand -base64 24 | tr -d '/+=' | head -c 32)}"
KEYCLOAK_DB_PASSWORD="${KEYCLOAK_DB_PASSWORD:-$(openssl rand -base64 24 | tr -d '/+=' | head -c 32)}"
OPENFGA_DB_PASSWORD="${OPENFGA_DB_PASSWORD:-$(openssl rand -base64 24 | tr -d '/+=' | head -c 32)}"
AGENTREGISTRY_DB_PASSWORD="${AGENTREGISTRY_DB_PASSWORD:-$(openssl rand -base64 24 | tr -d '/+=' | head -c 32)}"

# ── Discover VPC and subnet info from EKS cluster (agentic-cp) ──
echo "Discovering VPC configuration from EKS cluster '$CLUSTER_NAME'..."
VPC_ID=$(aws eks describe-cluster --name "$CLUSTER_NAME" --region "$REGION" \
  --query 'cluster.resourcesVpcConfig.vpcId' --output text)
echo "  VPC ID: $VPC_ID"

# CAPA-provisioned clusters do NOT use CloudFormation tags.
# Discover private subnets by map-public-ip-on-launch=false filter.
PRIVATE_SUBNETS=$(aws ec2 describe-subnets \
  --filters \
    "Name=vpc-id,Values=$VPC_ID" \
    "Name=map-public-ip-on-launch,Values=false" \
  --query 'Subnets[].SubnetId' --output text --region "$REGION")
SUBNET_IDS=$(echo "$PRIVATE_SUBNETS" | tr '\t' ',')
echo "  Private subnets: $SUBNET_IDS"

# ── Create security group for RDS/ElastiCache ──
echo "Creating security group for managed services..."
SG_ID=$(aws ec2 create-security-group \
  --group-name "$SG_NAME" \
  --description "Security group for RDS and ElastiCache used by agentic control-plane" \
  --vpc-id "$VPC_ID" \
  --region "$REGION" \
  --query 'GroupId' --output text 2>/dev/null || \
  aws ec2 describe-security-groups \
    --filters "Name=group-name,Values=$SG_NAME" "Name=vpc-id,Values=$VPC_ID" \
    --query 'SecurityGroups[0].GroupId' --output text --region "$REGION")
echo "  Security Group: $SG_ID"

# Allow inbound 5432/6379 from ALL cluster VPC CIDRs (10.0–10.4.0.0/16)
# Covers cross-cluster access via Transit Gateway: cp (10.1), obs (10.2), cell-* (10.3-10.4), mgmt (10.0)
for CIDR in 10.0.0.0/16 10.1.0.0/16 10.2.0.0/16 10.3.0.0/16 10.4.0.0/16; do
  aws ec2 authorize-security-group-ingress \
    --group-id "$SG_ID" \
    --protocol tcp --port 5432 --cidr "$CIDR" \
    --region "$REGION" 2>/dev/null || true
  aws ec2 authorize-security-group-ingress \
    --group-id "$SG_ID" \
    --protocol tcp --port 6379 --cidr "$CIDR" \
    --region "$REGION" 2>/dev/null || true
done
echo "  Ingress rules set for 5432/6379 from all cluster VPC CIDRs."

# ── 1. Create RDS PostgreSQL ──
echo ""
echo "Creating RDS PostgreSQL instance ($DB_INSTANCE_ID)..."
aws rds create-db-subnet-group \
  --db-subnet-group-name "$DB_SUBNET_GROUP" \
  --db-subnet-group-description "Subnet group for agentic-cp RDS" \
  --subnet-ids $PRIVATE_SUBNETS \
  --region "$REGION" 2>/dev/null || echo "  DB subnet group already exists."

MULTI_AZ_FLAG="--no-multi-az"
if [[ "$DB_MULTI_AZ" == "true" ]]; then
  MULTI_AZ_FLAG="--multi-az"
fi

aws rds create-db-instance \
  --db-instance-identifier "$DB_INSTANCE_ID" \
  --db-instance-class "$DB_INSTANCE_CLASS" \
  --engine postgres \
  --engine-version "$DB_ENGINE_VERSION" \
  --master-username "$DB_MASTER_USERNAME" \
  --master-user-password "$DB_PASSWORD" \
  --allocated-storage "$DB_ALLOCATED_STORAGE" \
  --storage-type "$DB_STORAGE_TYPE" \
  --storage-encrypted \
  --vpc-security-group-ids "$SG_ID" \
  --db-subnet-group-name "$DB_SUBNET_GROUP" \
  --db-name "langfuse" \
  $MULTI_AZ_FLAG \
  --backup-retention-period "$DB_BACKUP_RETENTION" \
  --region "$REGION" \
  --tags "Key=project,Value=agentic-platform" "Key=cluster,Value=agentic-cp" \
  2>/dev/null || echo "  RDS instance already exists."

echo "  Waiting for RDS to become available (this may take several minutes)..."
aws rds wait db-instance-available --db-instance-identifier "$DB_INSTANCE_ID" --region "$REGION"

RDS_ENDPOINT=$(aws rds describe-db-instances \
  --db-instance-identifier "$DB_INSTANCE_ID" \
  --query 'DBInstances[0].Endpoint.Address' --output text --region "$REGION")
echo "  RDS Endpoint: $RDS_ENDPOINT"

# ── 2. Create ElastiCache Redis ──
echo ""
echo "Creating ElastiCache Redis replication group ($REDIS_GROUP_ID)..."
aws elasticache create-cache-subnet-group \
  --cache-subnet-group-name "$CACHE_SUBNET_GROUP" \
  --cache-subnet-group-description "Subnet group for agentic-cp ElastiCache" \
  --subnet-ids $PRIVATE_SUBNETS \
  --region "$REGION" 2>/dev/null || echo "  Cache subnet group already exists."

aws elasticache create-replication-group \
  --replication-group-id "$REDIS_GROUP_ID" \
  --replication-group-description "Redis for agentic control-plane (Langfuse + services)" \
  --engine redis \
  --cache-node-type "$REDIS_NODE_TYPE" \
  --num-cache-clusters "$REDIS_NUM_CLUSTERS" \
  --transit-encryption-enabled \
  --auth-token "$REDIS_PASSWORD" \
  --cache-subnet-group-name "$CACHE_SUBNET_GROUP" \
  --security-group-ids "$SG_ID" \
  --region "$REGION" \
  --tags "Key=project,Value=agentic-platform" "Key=cluster,Value=agentic-cp" \
  2>/dev/null || echo "  Redis replication group already exists."

echo "  Waiting for Redis to become available..."
aws elasticache wait replication-group-available \
  --replication-group-id "$REDIS_GROUP_ID" --region "$REGION"

REDIS_ENDPOINT=$(aws elasticache describe-replication-groups \
  --replication-group-id "$REDIS_GROUP_ID" \
  --query 'ReplicationGroups[0].NodeGroups[0].PrimaryEndpoint.Address' --output text --region "$REGION")
echo "  Redis Endpoint: $REDIS_ENDPOINT"

# ── 3. Create S3 Bucket ──
echo ""
echo "Creating S3 bucket for Langfuse ($S3_BUCKET)..."
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
echo "  S3 bucket ready: $S3_BUCKET"

# ── 4. Create additional databases on the RDS instance ──
# Uses kubectl run with a temporary psql pod against the agentic-cp context.
# The master user (postgres) is used to bootstrap per-service users.
echo ""
echo "Creating additional databases on RDS via in-cluster psql pod (context: agentic-cp)..."
PSQL_BASE="kubectl --context agentic-cp run"

run_psql() {
  local pod_name="$1"; shift
  local pgpassword="$1"; shift
  local dsn="$1"; shift
  $PSQL_BASE "$pod_name" --rm -i --restart=Never \
    --image=postgres:17-alpine \
    --env="PGPASSWORD=${pgpassword}" \
    -- psql "$dsn" "$@" \
    2>/dev/null || echo "  (pod $pod_name: commands may already be applied)"
}

# ── keycloak ──
echo ""
echo "  Setting up keycloak database and user..."
run_psql psql-kc-db "$DB_PASSWORD" \
  "postgresql://${DB_MASTER_USERNAME}:${DB_PASSWORD}@${RDS_ENDPOINT}:5432/langfuse" \
  -c "CREATE DATABASE keycloak;"

run_psql psql-kc-user "$DB_PASSWORD" \
  "postgresql://${DB_MASTER_USERNAME}:${DB_PASSWORD}@${RDS_ENDPOINT}:5432/langfuse" \
  -c "CREATE USER keycloak WITH PASSWORD '${KEYCLOAK_DB_PASSWORD}';" \
  -c "GRANT ALL PRIVILEGES ON DATABASE keycloak TO keycloak;" \
  -c "ALTER DATABASE keycloak OWNER TO keycloak;"

run_psql psql-kc-schema "$KEYCLOAK_DB_PASSWORD" \
  "postgresql://keycloak:${KEYCLOAK_DB_PASSWORD}@${RDS_ENDPOINT}:5432/keycloak" \
  -c "GRANT ALL ON SCHEMA public TO keycloak;" \
  -c "ALTER SCHEMA public OWNER TO keycloak;"
echo "  keycloak database and user ready."

# ── openfga ──
echo ""
echo "  Setting up openfga database and user..."
run_psql psql-fga-db "$DB_PASSWORD" \
  "postgresql://${DB_MASTER_USERNAME}:${DB_PASSWORD}@${RDS_ENDPOINT}:5432/langfuse" \
  -c "CREATE DATABASE openfga;"

run_psql psql-fga-user "$DB_PASSWORD" \
  "postgresql://${DB_MASTER_USERNAME}:${DB_PASSWORD}@${RDS_ENDPOINT}:5432/langfuse" \
  -c "CREATE USER openfga WITH PASSWORD '${OPENFGA_DB_PASSWORD}';" \
  -c "GRANT ALL PRIVILEGES ON DATABASE openfga TO openfga;" \
  -c "ALTER DATABASE openfga OWNER TO openfga;"

run_psql psql-fga-schema "$OPENFGA_DB_PASSWORD" \
  "postgresql://openfga:${OPENFGA_DB_PASSWORD}@${RDS_ENDPOINT}:5432/openfga" \
  -c "GRANT ALL ON SCHEMA public TO openfga;" \
  -c "ALTER SCHEMA public OWNER TO openfga;"
echo "  openfga database and user ready."

# ── agentregistry ──
echo ""
echo "  Setting up agentregistry database and user..."
run_psql psql-ar-db "$DB_PASSWORD" \
  "postgresql://${DB_MASTER_USERNAME}:${DB_PASSWORD}@${RDS_ENDPOINT}:5432/langfuse" \
  -c "CREATE DATABASE agentregistry;"

run_psql psql-ar-user "$DB_PASSWORD" \
  "postgresql://${DB_MASTER_USERNAME}:${DB_PASSWORD}@${RDS_ENDPOINT}:5432/langfuse" \
  -c "CREATE USER agentregistry WITH PASSWORD '${AGENTREGISTRY_DB_PASSWORD}';" \
  -c "GRANT ALL PRIVILEGES ON DATABASE agentregistry TO agentregistry;" \
  -c "ALTER DATABASE agentregistry OWNER TO agentregistry;"

echo "  Enabling pgvector extension for agentregistry..."
run_psql psql-ar-ext "$DB_PASSWORD" \
  "postgresql://${DB_MASTER_USERNAME}:${DB_PASSWORD}@${RDS_ENDPOINT}:5432/agentregistry" \
  -c "CREATE EXTENSION IF NOT EXISTS vector;"

run_psql psql-ar-schema "$AGENTREGISTRY_DB_PASSWORD" \
  "postgresql://agentregistry:${AGENTREGISTRY_DB_PASSWORD}@${RDS_ENDPOINT}:5432/agentregistry" \
  -c "GRANT ALL ON SCHEMA public TO agentregistry;" \
  -c "ALTER SCHEMA public OWNER TO agentregistry;"
echo "  agentregistry database and user ready."

# ── 5. Save outputs to .env.cp ──
echo ""
echo "=== AWS Resources Created ==="
ENV_FILE="$ROOT_DIR/.env.cp"

# Write/overwrite the env file with all outputs
cat > "$ENV_FILE" <<EOF
# Control-plane AWS resource outputs
# Generated by scripts/phase3a/00-create-aws-resources.sh
# $(date -u +"%Y-%m-%dT%H:%M:%SZ")

# ── RDS ──
RDS_ENDPOINT=${RDS_ENDPOINT}
DB_PASSWORD=${DB_PASSWORD}
RDS_DATABASE_URL=postgresql://${DB_MASTER_USERNAME}:${DB_PASSWORD}@${RDS_ENDPOINT}:5432/langfuse
KEYCLOAK_DB_PASSWORD=${KEYCLOAK_DB_PASSWORD}
KEYCLOAK_DATABASE_URL=postgresql://keycloak:${KEYCLOAK_DB_PASSWORD}@${RDS_ENDPOINT}:5432/keycloak
OPENFGA_DB_PASSWORD=${OPENFGA_DB_PASSWORD}
OPENFGA_DATABASE_URL=postgresql://openfga:${OPENFGA_DB_PASSWORD}@${RDS_ENDPOINT}:5432/openfga
AGENTREGISTRY_DB_PASSWORD=${AGENTREGISTRY_DB_PASSWORD}
AGENTREGISTRY_DATABASE_URL=postgresql://agentregistry:${AGENTREGISTRY_DB_PASSWORD}@${RDS_ENDPOINT}:5432/agentregistry

# ── ElastiCache ──
REDIS_ENDPOINT=${REDIS_ENDPOINT}
REDIS_PASSWORD=${REDIS_PASSWORD}
REDIS_URL=rediss://:${REDIS_PASSWORD}@${REDIS_ENDPOINT}:6379

# ── S3 ──
S3_BUCKET=${S3_BUCKET}
S3_REGION=${REGION}
EOF

echo "AWS outputs written to $ENV_FILE"
echo ""
echo "Next step: ./scripts/phase3a/01-create-secrets.sh"
