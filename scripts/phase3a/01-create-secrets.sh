#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

echo "=== Phase 3a: Creating Kubernetes Secrets on Control-Plane Cluster ==="

# Load .env.cp (AWS outputs from 00-create-aws-resources.sh)
ENV_FILE="$ROOT_DIR/.env.cp"
if [[ ! -f "$ENV_FILE" ]]; then
  echo "ERROR: $ENV_FILE not found. Run 00-create-aws-resources.sh first."
  exit 1
fi
set -a; source "$ENV_FILE"; set +a

if [[ -z "${RDS_ENDPOINT:-}" ]]; then
  echo "ERROR: RDS_ENDPOINT not set in .env.cp. Run 00-create-aws-resources.sh first."
  exit 1
fi

KUBECTL="kubectl --context agentic-cp"

# ── Create namespaces ──
echo "Creating namespaces..."
$KUBECTL apply -f "$ROOT_DIR/platform/control-plane-manifests/namespaces.yaml"

# ── Generate secrets (idempotent: only generate if not already in .env.cp) ──
echo "Generating platform secrets..."

if [[ -z "${NEXTAUTH_SECRET:-}" ]]; then
  NEXTAUTH_SECRET=$(openssl rand -base64 32)
  echo "NEXTAUTH_SECRET=$NEXTAUTH_SECRET" >> "$ENV_FILE"
fi

if [[ -z "${ENCRYPTION_KEY:-}" ]]; then
  ENCRYPTION_KEY=$(openssl rand -hex 32)
  echo "ENCRYPTION_KEY=$ENCRYPTION_KEY" >> "$ENV_FILE"
fi

if [[ -z "${SALT:-}" ]]; then
  SALT=$(openssl rand -base64 32)
  echo "SALT=$SALT" >> "$ENV_FILE"
fi

# ── 1. langfuse/langfuse-db-credentials ──
echo "Creating secret: langfuse/langfuse-db-credentials..."
$KUBECTL create secret generic langfuse-db-credentials \
  --namespace langfuse \
  --from-literal=password="$DB_PASSWORD" \
  --from-literal=DATABASE_URL="postgresql://langfuse:${DB_PASSWORD}@${RDS_ENDPOINT}:5432/langfuse?sslmode=require" \
  --dry-run=client -o yaml | $KUBECTL apply -f -

# ── 2. langfuse/langfuse-secrets ──
echo "Creating secret: langfuse/langfuse-secrets..."
$KUBECTL create secret generic langfuse-secrets \
  --namespace langfuse \
  --from-literal=NEXTAUTH_SECRET="$NEXTAUTH_SECRET" \
  --from-literal=ENCRYPTION_KEY="$ENCRYPTION_KEY" \
  --from-literal=SALT="$SALT" \
  --from-literal=S3_BUCKET_NAME="${S3_BUCKET}" \
  --from-literal=S3_REGION="${S3_REGION}" \
  --from-literal=S3_ACCESS_KEY_ID="${S3_ACCESS_KEY_ID:-}" \
  --from-literal=S3_SECRET_ACCESS_KEY="${S3_SECRET_ACCESS_KEY:-}" \
  --dry-run=client -o yaml | $KUBECTL apply -f -

# ── 3. keycloak/keycloak-db-credentials ──
echo "Creating secret: keycloak/keycloak-db-credentials..."
$KUBECTL create secret generic keycloak-db-credentials \
  --namespace keycloak \
  --from-literal=password="$KEYCLOAK_DB_PASSWORD" \
  --dry-run=client -o yaml | $KUBECTL apply -f -

# ── 4. openfga/openfga-db-credentials ──
echo "Creating secret: openfga/openfga-db-credentials..."
$KUBECTL create secret generic openfga-db-credentials \
  --namespace openfga \
  --from-literal=password="$OPENFGA_DB_PASSWORD" \
  --from-literal=uri="postgres://openfga:${OPENFGA_DB_PASSWORD}@${RDS_ENDPOINT}:5432/openfga?sslmode=require" \
  --dry-run=client -o yaml | $KUBECTL apply -f -

# ── 5. keycloak/keycloak-admin-credentials ──
echo "Creating secret: keycloak/keycloak-admin-credentials..."
KEYCLOAK_ADMIN_PASSWORD=$(openssl rand -base64 24 | tr -d '/+=' | head -c 32)
$KUBECTL create secret generic keycloak-admin-credentials \
  --namespace keycloak \
  --from-literal=user="admin" \
  --from-literal=password="$KEYCLOAK_ADMIN_PASSWORD" \
  --dry-run=client -o yaml | $KUBECTL apply -f -

# ── 6. langfuse/langfuse-clickhouse-credentials ──
echo "Creating secret: langfuse/langfuse-clickhouse-credentials..."
CLICKHOUSE_PASSWORD=$(openssl rand -base64 24 | tr -d '/+=' | head -c 32)
$KUBECTL create secret generic langfuse-clickhouse-credentials \
  --namespace langfuse \
  --from-literal=CLICKHOUSE_PASSWORD="$CLICKHOUSE_PASSWORD" \
  --dry-run=client -o yaml | $KUBECTL apply -f -

# ── 6. agentregistry/agentregistry-db-credentials ──
echo "Creating secret: agentregistry/agentregistry-db-credentials..."
$KUBECTL create secret generic agentregistry-db-credentials \
  --namespace agentregistry \
  --from-literal=AGENT_REGISTRY_DATABASE_URL="postgresql://agentregistry:${AGENTREGISTRY_DB_PASSWORD}@${RDS_ENDPOINT}:5432/agentregistry?sslmode=require" \
  --dry-run=client -o yaml | $KUBECTL apply -f -

echo ""
echo "=== Secrets created on agentic-cp ==="
echo "All generated values appended to $ENV_FILE (DO NOT COMMIT)"
echo ""
echo "Next step: ./02-deploy-control-plane.sh"
