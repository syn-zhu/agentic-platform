#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

echo "=== Phase 2: Creating Namespaces and Kubernetes Secrets ==="

# Load .env (user-provided vars + AWS outputs from step 01)
ENV_FILE="$ROOT_DIR/.env"
if [[ ! -f "$ENV_FILE" ]]; then
  echo "ERROR: $ENV_FILE not found. Create it with ANTHROPIC_API_KEY, then run 01-create-aws-resources.sh."
  exit 1
fi
set -a; source "$ENV_FILE"; set +a

if [[ -z "${RDS_ENDPOINT:-}" ]]; then
  echo "ERROR: RDS_ENDPOINT not set in .env. Run 01-create-aws-resources.sh first."
  exit 1
fi

# ── Create namespaces ──
echo "Creating namespaces..."
kubectl apply -f "$ROOT_DIR/platform/manifests/namespaces.yaml"

# ── Substitute AWS endpoints into Helm values files ──
echo "Updating Helm values with actual AWS endpoints..."
sed -i.bak "s|PLACEHOLDER_RDS_ENDPOINT|${RDS_ENDPOINT}|g" \
  "$ROOT_DIR/platform/values/langfuse.yaml" \
  "$ROOT_DIR/platform/values/keycloak.yaml"
sed -i.bak "s|PLACEHOLDER_REDIS_ENDPOINT|${REDIS_ENDPOINT}|g" \
  "$ROOT_DIR/platform/values/langfuse.yaml"
# Clean up backup files
rm -f "$ROOT_DIR/platform/values/"*.bak
echo "  Updated langfuse.yaml and keycloak.yaml with RDS/Redis endpoints."

# ── Generate secrets ──
echo "Generating platform secrets..."

NEXTAUTH_SECRET=$(openssl rand -base64 32)
SALT=$(openssl rand -base64 32)
ENCRYPTION_KEY=$(openssl rand -hex 32)
ADMIN_PASSWORD=$(openssl rand -base64 16 | tr -d '/+=' | head -c 20)
CLICKHOUSE_PASSWORD=$(openssl rand -base64 16 | tr -d '/+=' | head -c 20)

# Langfuse API keys — match Langfuse's internal format (pk-lf-<uuid>, sk-lf-<uuid>).
# These are passed to Langfuse via LANGFUSE_INIT_* env vars so it registers them on first boot.
LANGFUSE_PUBLIC_KEY="pk-lf-$(uuidgen | tr '[:upper:]' '[:lower:]')"
LANGFUSE_SECRET_KEY="sk-lf-$(uuidgen | tr '[:upper:]' '[:lower:]')"

# Base64 encode for Basic auth: public_key:secret_key
LANGFUSE_BASIC_AUTH=$(echo -n "${LANGFUSE_PUBLIC_KEY}:${LANGFUSE_SECRET_KEY}" | base64 -w0 2>/dev/null || echo -n "${LANGFUSE_PUBLIC_KEY}:${LANGFUSE_SECRET_KEY}" | base64)

# ── 1. Langfuse namespace secrets ──
echo "Creating secrets in langfuse namespace..."

kubectl create secret generic langfuse-db-credentials \
  --namespace langfuse \
  --from-literal=DATABASE_URL="$RDS_DATABASE_URL" \
  --from-literal=password="$RDS_PASSWORD" \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl create secret generic langfuse-redis-credentials \
  --namespace langfuse \
  --from-literal=REDIS_HOST="$REDIS_ENDPOINT" \
  --from-literal=REDIS_AUTH="$REDIS_PASSWORD" \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl create secret generic langfuse-clickhouse-credentials \
  --namespace langfuse \
  --from-literal=CLICKHOUSE_USER="default" \
  --from-literal=CLICKHOUSE_PASSWORD="$CLICKHOUSE_PASSWORD" \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl create secret generic langfuse-auth-secrets \
  --namespace langfuse \
  --from-literal=NEXTAUTH_SECRET="$NEXTAUTH_SECRET" \
  --from-literal=SALT="$SALT" \
  --from-literal=ENCRYPTION_KEY="$ENCRYPTION_KEY" \
  --from-literal=ADMIN_PASSWORD="$ADMIN_PASSWORD" \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl create secret generic langfuse-api-keys \
  --namespace langfuse \
  --from-literal=PUBLIC_KEY="$LANGFUSE_PUBLIC_KEY" \
  --from-literal=SECRET_KEY="$LANGFUSE_SECRET_KEY" \
  --dry-run=client -o yaml | kubectl apply -f -

# OTEL collector auth (gRPC-to-HTTP bridge for kagent → Langfuse)
kubectl create secret generic otel-collector-auth \
  --namespace langfuse \
  --from-literal=AUTH_HEADER="Basic ${LANGFUSE_BASIC_AUTH}" \
  --dry-run=client -o yaml | kubectl apply -f -

# ── 2. kagent-system namespace secrets ──
echo "Creating secrets in kagent-system namespace..."

# OTEL auth header for Langfuse (used by kagent controller + agent pods)
kubectl create secret generic langfuse-otel-auth \
  --namespace kagent-system \
  --from-literal=OTEL_HEADERS="Authorization=Basic ${LANGFUSE_BASIC_AUTH}" \
  --dry-run=client -o yaml | kubectl apply -f -

# ── 3. keycloak namespace secrets ──
echo "Creating secrets in keycloak namespace..."

KEYCLOAK_ADMIN_PASSWORD=$(openssl rand -base64 16 | tr -d '/+=' | head -c 20)

# Keycloak shares the RDS instance with Langfuse but uses a dedicated 'keycloak' DB user
# with access only to the 'keycloak' database (created by 01-create-aws-resources.sh).
kubectl create secret generic keycloak-db-credentials \
  --namespace keycloak \
  --from-literal=password="$KEYCLOAK_DB_PASSWORD" \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl create secret generic keycloak-admin-credentials \
  --namespace keycloak \
  --from-literal=admin-password="$KEYCLOAK_ADMIN_PASSWORD" \
  --dry-run=client -o yaml | kubectl apply -f -

# ── 4. agentgateway-system secrets ──
echo "Creating secrets in agentgateway-system namespace..."

# Same Langfuse OTEL auth for agentgateway (referenced in AgentgatewayParameters)
kubectl create secret generic langfuse-otel-auth \
  --namespace agentgateway-system \
  --from-literal=OTEL_HEADERS="Authorization=Basic ${LANGFUSE_BASIC_AUTH}" \
  --from-literal=BASIC_AUTH="Basic ${LANGFUSE_BASIC_AUTH}" \
  --dry-run=client -o yaml | kubectl apply -f -

# ── 5. agentregistry namespace secrets ──
echo "Creating secrets in agentregistry namespace..."

AGENTREGISTRY_JWT_KEY=$(openssl rand -hex 32)

kubectl create secret generic agentregistry-db-credentials \
  --namespace agentregistry \
  --from-literal=AGENT_REGISTRY_DATABASE_URL="postgresql://agentregistry:${AGENTREGISTRY_DB_PASSWORD}@${RDS_ENDPOINT}:5432/agentregistry?sslmode=require" \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl create secret generic agentregistry-auth \
  --namespace agentregistry \
  --from-literal=AGENT_REGISTRY_JWT_PRIVATE_KEY="$AGENTREGISTRY_JWT_KEY" \
  --dry-run=client -o yaml | kubectl apply -f -

# OpenAI API key for semantic search embeddings (optional — leave blank to skip)
if [[ -n "${OPENAI_API_KEY:-}" ]]; then
  echo "Creating OpenAI secret for agentregistry embeddings..."
  kubectl create secret generic agentregistry-openai \
    --namespace agentregistry \
    --from-literal=AGENT_REGISTRY_OPENAI_API_KEY="$OPENAI_API_KEY" \
    --dry-run=client -o yaml | kubectl apply -f -
else
  echo "OPENAI_API_KEY not set — skipping agentregistry embeddings secret."
  echo "  To enable semantic search later, set OPENAI_API_KEY and re-run this script."
fi

# ── Append generated secrets to .env ──
cat >> "$ENV_FILE" <<EOF

# ── Platform Secrets (generated by 02-create-secrets.sh) ──
NEXTAUTH_SECRET=$NEXTAUTH_SECRET
SALT=$SALT
ENCRYPTION_KEY=$ENCRYPTION_KEY
ADMIN_PASSWORD=$ADMIN_PASSWORD
CLICKHOUSE_PASSWORD=$CLICKHOUSE_PASSWORD
LANGFUSE_PUBLIC_KEY=$LANGFUSE_PUBLIC_KEY
LANGFUSE_SECRET_KEY=$LANGFUSE_SECRET_KEY
LANGFUSE_BASIC_AUTH=$LANGFUSE_BASIC_AUTH
KEYCLOAK_ADMIN_PASSWORD=$KEYCLOAK_ADMIN_PASSWORD
AGENTREGISTRY_JWT_KEY=$AGENTREGISTRY_JWT_KEY
EOF

echo ""
echo "=== Secrets created ==="
echo "All values appended to $ENV_FILE (DO NOT COMMIT)"
echo ""
echo "Langfuse admin login:"
echo "  Email:    admin@platform.internal"
echo "  Password: $ADMIN_PASSWORD"
echo ""
echo "Keycloak admin login:"
echo "  User:     admin"
echo "  Password: $KEYCLOAK_ADMIN_PASSWORD"
echo ""
echo "Next step: ./03-deploy-platform.sh"
