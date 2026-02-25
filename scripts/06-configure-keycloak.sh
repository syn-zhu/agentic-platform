#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

echo "=== Phase 6: Configuring Keycloak ==="

# Load secrets
SECRETS_FILE="$ROOT_DIR/.secrets.env"
if [[ ! -f "$SECRETS_FILE" ]]; then
  echo "ERROR: $SECRETS_FILE not found. Run 02-create-secrets.sh first."
  exit 1
fi
set -a; source "$SECRETS_FILE"; set +a

KEYCLOAK_URL="http://keycloak.keycloak.svc.cluster.local:8080"
ADMIN_USER="admin"
ADMIN_PASS="${KEYCLOAK_ADMIN_PASSWORD}"
REALM_FILE="$ROOT_DIR/platform/manifests/keycloak-agents-realm.json"

if [[ ! -f "$REALM_FILE" ]]; then
  echo "ERROR: Realm file not found at $REALM_FILE"
  exit 1
fi

# ── Wait for Keycloak to be ready ──
echo "Waiting for Keycloak to be ready..."
kubectl rollout status deployment/keycloak -n keycloak --timeout=180s >/dev/null 2>&1 || \
  kubectl rollout status statefulset/keycloak -n keycloak --timeout=180s >/dev/null 2>&1

# ── Get admin token ──
echo "Authenticating to Keycloak admin API..."
ADMIN_TOKEN=$(kubectl run keycloak-auth --rm -i --restart=Never \
  --image=curlimages/curl:latest -n keycloak \
  -- curl -s -X POST "${KEYCLOAK_URL}/realms/master/protocol/openid-connect/token" \
  -d "client_id=admin-cli" \
  -d "username=${ADMIN_USER}" \
  -d "password=${ADMIN_PASS}" \
  -d "grant_type=password" 2>/dev/null \
  | python3 -c "import sys,json; print(json.load(sys.stdin).get('access_token',''))" 2>/dev/null)

if [[ -z "$ADMIN_TOKEN" ]]; then
  echo "ERROR: Failed to get admin token from Keycloak."
  echo "  Verify Keycloak is running and admin credentials are correct."
  exit 1
fi
echo "  Authenticated."

# ── Create ConfigMap with realm JSON (for use inside the cluster) ──
echo "Creating realm ConfigMap..."
kubectl create configmap keycloak-realm-json \
  --namespace keycloak \
  --from-file=realm.json="$REALM_FILE" \
  --dry-run=client -o yaml | kubectl apply -f -

# ── Import or update the agents realm ──
echo "Importing agents realm..."

# Check if realm already exists
REALM_EXISTS=$(kubectl run keycloak-realm-check --rm -i --restart=Never \
  --image=curlimages/curl:latest -n keycloak \
  -- curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer ${ADMIN_TOKEN}" \
  "${KEYCLOAK_URL}/admin/realms/agents" 2>/dev/null || echo "000")

if [[ "$REALM_EXISTS" == "200" ]]; then
  echo "  Realm 'agents' already exists — updating via partial import..."
  # Use partialImport to merge clients, scopes, users without destructive overwrite
  kubectl run keycloak-realm-import --rm -i --restart=Never \
    --image=curlimages/curl:latest -n keycloak \
    --overrides='{
      "spec": {
        "volumes": [{"name": "realm", "configMap": {"name": "keycloak-realm-json"}}],
        "containers": [{
          "name": "keycloak-realm-import",
          "image": "curlimages/curl:latest",
          "command": ["curl", "-s", "-X", "POST",
            "-H", "Authorization: Bearer '"${ADMIN_TOKEN}"'",
            "-H", "Content-Type: application/json",
            "-d", "@/realm/realm.json",
            "'"${KEYCLOAK_URL}/admin/realms/agents/partialImport"'"],
          "volumeMounts": [{"name": "realm", "mountPath": "/realm", "readOnly": true}]
        }]
      }
    }' 2>/dev/null || echo "  Partial import completed (some resources may already exist)."
else
  echo "  Creating new 'agents' realm..."
  kubectl run keycloak-realm-create --rm -i --restart=Never \
    --image=curlimages/curl:latest -n keycloak \
    --overrides='{
      "spec": {
        "volumes": [{"name": "realm", "configMap": {"name": "keycloak-realm-json"}}],
        "containers": [{
          "name": "keycloak-realm-create",
          "image": "curlimages/curl:latest",
          "command": ["curl", "-s", "-X", "POST",
            "-H", "Authorization: Bearer '"${ADMIN_TOKEN}"'",
            "-H", "Content-Type: application/json",
            "-d", "@/realm/realm.json",
            "'"${KEYCLOAK_URL}/admin/realms"'"],
          "volumeMounts": [{"name": "realm", "mountPath": "/realm", "readOnly": true}]
        }]
      }
    }' 2>/dev/null || echo "  Realm creation may have partially succeeded."
fi

# ── Verify OIDC discovery ──
echo ""
echo "Verifying OIDC discovery endpoint..."
OIDC_STATUS=$(kubectl run keycloak-oidc-check --rm -i --restart=Never \
  --image=curlimages/curl:latest -n keycloak \
  -- curl -s -o /dev/null -w "%{http_code}" \
  "${KEYCLOAK_URL}/realms/agents/.well-known/openid-configuration" 2>/dev/null || echo "000")

if [[ "$OIDC_STATUS" == "200" ]]; then
  echo "  ✓ OIDC discovery endpoint is accessible."
else
  echo "  ✗ OIDC discovery returned HTTP ${OIDC_STATUS}. The realm may not have been created properly."
fi

# ── Verify agentregistry client exists ──
echo "Verifying agentregistry client..."
CLIENT_CHECK=$(kubectl run keycloak-client-check --rm -i --restart=Never \
  --image=curlimages/curl:latest -n keycloak \
  -- curl -s \
  -H "Authorization: Bearer ${ADMIN_TOKEN}" \
  "${KEYCLOAK_URL}/admin/realms/agents/clients?clientId=agentregistry" 2>/dev/null \
  | python3 -c "import sys,json; clients=json.load(sys.stdin); print('found' if clients else 'missing')" 2>/dev/null || echo "error")

if [[ "$CLIENT_CHECK" == "found" ]]; then
  echo "  ✓ agentregistry client exists in agents realm."
else
  echo "  ✗ agentregistry client not found (status: ${CLIENT_CHECK}). Check realm import."
fi

echo ""
echo "=== Keycloak configuration complete ==="
echo ""
echo "OIDC Issuer:  ${KEYCLOAK_URL}/realms/agents"
echo "Clients:      agent-gateway, agentregistry"
echo ""
echo "Test token exchange:"
echo "  curl -s -X POST ${KEYCLOAK_URL}/realms/agents/protocol/openid-connect/token \\"
echo "    -d 'client_id=agentregistry' \\"
echo "    -d 'username=testuser' \\"
echo "    -d 'password=testpass123' \\"
echo "    -d 'grant_type=password'"
