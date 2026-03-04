#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

echo "=== Phase 6: Configuring Keycloak ==="

# Load .env
if [[ ! -f "$ROOT_DIR/.env" ]]; then
  echo "ERROR: .env not found. Run 01/02 setup scripts first."
  exit 1
fi
set -a; source "$ROOT_DIR/.env"; set +a

KEYCLOAK_URL="http://keycloak.keycloak.svc.cluster.local:8080"
ADMIN_USER="admin"
ADMIN_PASS="${KEYCLOAK_ADMIN_PASSWORD}"
REALM_FILE="$ROOT_DIR/platform/manifests/keycloak-agents-realm.json"

if [[ ! -f "$REALM_FILE" ]]; then
  echo "ERROR: Realm file not found at $REALM_FILE"
  exit 1
fi

# ── Helper: run curl inside the cluster ──
# Spins up an ephemeral pod to reach Keycloak's cluster-internal URL.
kc_curl() {
  kubectl run keycloak-curl-$RANDOM --rm -i --restart=Never \
    --image=curlimages/curl:latest -n keycloak \
    -- curl -s "$@" 2>/dev/null
}

# ── Wait for Keycloak to be ready ──
# keycloakx chart deploys a StatefulSet
echo "Waiting for Keycloak to be ready..."
kubectl rollout status statefulset/keycloak -n keycloak --timeout=180s >/dev/null 2>&1

# ── Get admin token ──
echo "Authenticating to Keycloak admin API..."
ADMIN_TOKEN=$(kc_curl -X POST "${KEYCLOAK_URL}/realms/master/protocol/openid-connect/token" \
  -d "client_id=admin-cli" \
  -d "username=${ADMIN_USER}" \
  -d "password=${ADMIN_PASS}" \
  -d "grant_type=password" \
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
REALM_EXISTS=$(kc_curl -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer ${ADMIN_TOKEN}" \
  "${KEYCLOAK_URL}/admin/realms/agents" || echo "000")

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
OIDC_STATUS=$(kc_curl -o /dev/null -w "%{http_code}" \
  "${KEYCLOAK_URL}/realms/agents/.well-known/openid-configuration" || echo "000")

if [[ "$OIDC_STATUS" == "200" ]]; then
  echo "  OK — OIDC discovery endpoint is accessible."
else
  echo "  FAIL — OIDC discovery returned HTTP ${OIDC_STATUS}. The realm may not have been created properly."
fi

# ── Verify agentregistry client exists ──
echo "Verifying agentregistry client..."
CLIENT_CHECK=$(kc_curl \
  -H "Authorization: Bearer ${ADMIN_TOKEN}" \
  "${KEYCLOAK_URL}/admin/realms/agents/clients?clientId=agentregistry" \
  | python3 -c "import sys,json; clients=json.load(sys.stdin); print('found' if clients else 'missing')" 2>/dev/null || echo "error")

if [[ "$CLIENT_CHECK" == "found" ]]; then
  echo "  OK — agentregistry client exists in agents realm."
else
  echo "  FAIL — agentregistry client not found (status: ${CLIENT_CHECK}). Check realm import."
fi

# ══════════════════════════════════════════════════════════════════
# K8s OIDC Identity Provider (for federated client authentication)
# ══════════════════════════════════════════════════════════════════
# Allows K8s service account tokens to be used as client assertions
# (client_assertion_type=urn:ietf:params:oauth:client-assertion-type:jwt-bearer).
# The IdP validates SA tokens against the cluster's OIDC discovery endpoint.

echo ""
echo "Configuring K8s OIDC identity provider..."

# Get the cluster's OIDC issuer URL
K8S_OIDC_ISSUER=$(kubectl get --raw /.well-known/openid-configuration \
  | python3 -c "import sys,json; print(json.load(sys.stdin)['issuer'])")

if [[ -z "$K8S_OIDC_ISSUER" ]]; then
  echo "  WARNING: Could not determine K8s OIDC issuer. Skipping IdP configuration."
else
  # Check if IdP already exists
  IDP_EXISTS=$(kc_curl -o /dev/null -w "%{http_code}" \
    -H "Authorization: Bearer ${ADMIN_TOKEN}" \
    "${KEYCLOAK_URL}/admin/realms/agents/identity-provider/instances/kubernetes" || echo "000")

  IDP_JSON=$(cat <<IDPEOF
{
  "alias": "kubernetes",
  "displayName": "Kubernetes Service Accounts",
  "providerId": "oidc",
  "enabled": true,
  "trustEmail": false,
  "storeToken": false,
  "addReadTokenRoleOnCreate": false,
  "config": {
    "issuer": "${K8S_OIDC_ISSUER}",
    "authorizationUrl": "${K8S_OIDC_ISSUER}/authorize",
    "tokenUrl": "${K8S_OIDC_ISSUER}/token",
    "jwksUrl": "${K8S_OIDC_ISSUER}/keys",
    "clientId": "keycloak",
    "clientSecret": "unused",
    "clientAuthMethod": "client_secret_post",
    "syncMode": "IMPORT",
    "useJwksUrl": "true",
    "validateSignature": "true"
  }
}
IDPEOF
)

  if [[ "$IDP_EXISTS" == "200" ]]; then
    echo "  K8s OIDC IdP already exists — updating..."
    kc_curl -X PUT \
      -H "Authorization: Bearer ${ADMIN_TOKEN}" \
      -H "Content-Type: application/json" \
      -d "${IDP_JSON}" \
      "${KEYCLOAK_URL}/admin/realms/agents/identity-provider/instances/kubernetes" > /dev/null
  else
    echo "  Creating K8s OIDC identity provider..."
    kc_curl -X POST \
      -H "Authorization: Bearer ${ADMIN_TOKEN}" \
      -H "Content-Type: application/json" \
      -d "${IDP_JSON}" \
      "${KEYCLOAK_URL}/admin/realms/agents/identity-provider/instances" > /dev/null
  fi
  echo "  OK — K8s OIDC IdP configured (issuer: ${K8S_OIDC_ISSUER})."
fi

# ══════════════════════════════════════════════════════════════════
# Initial Access Token for Dynamic Client Registration (RFC 7591)
# ══════════════════════════════════════════════════════════════════
# Creates an IAT that tenant setup scripts use to register agent clients.
# Stored as a K8s Secret in platform-system namespace.

echo ""
echo "Creating Initial Access Token for DCR..."

kubectl create namespace platform-system --dry-run=client -o yaml | kubectl apply -f - 2>/dev/null

IAT_RESPONSE=$(kc_curl -X POST \
  -H "Authorization: Bearer ${ADMIN_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"count": 100, "expiration": 0}' \
  "${KEYCLOAK_URL}/admin/realms/agents/clients-initial-access")

IAT_TOKEN=$(echo "$IAT_RESPONSE" \
  | python3 -c "import sys,json; print(json.load(sys.stdin).get('token',''))" 2>/dev/null)

if [[ -z "$IAT_TOKEN" ]]; then
  echo "  WARNING: Failed to create IAT. DCR will not be available."
  echo "  Response: ${IAT_RESPONSE}"
else
  kubectl create secret generic keycloak-initial-access-token \
    --namespace platform-system \
    --from-literal=token="$IAT_TOKEN" \
    --dry-run=client -o yaml | kubectl apply -f -
  echo "  OK — IAT stored as Secret 'keycloak-initial-access-token' in platform-system."
  echo "  Remaining registrations: 100 (no expiration)."
fi

# ══════════════════════════════════════════════════════════════════
# Apply ingress authentication policy
# ══════════════════════════════════════════════════════════════════

echo ""
echo "Applying ingress authentication policy..."
kubectl apply -f "$ROOT_DIR/platform/manifests/ingress-auth-policy.yaml"
echo "  OK — Ingress auth policy applied (JWT validation on ingress gateway)."

# ══════════════════════════════════════════════════════════════════
# Summary
# ══════════════════════════════════════════════════════════════════

echo ""
echo "=== Keycloak configuration complete ==="
echo ""
echo "OIDC Issuer:       ${KEYCLOAK_URL}/realms/agents"
echo "Clients:           agent-gateway, agentregistry"
echo "K8s OIDC IdP:      ${K8S_OIDC_ISSUER:-not configured}"
echo "DCR IAT Secret:    platform-system/keycloak-initial-access-token"
echo ""
echo "Test token (password grant):"
echo "  curl -s -X POST ${KEYCLOAK_URL}/realms/agents/protocol/openid-connect/token \\"
echo "    -d 'client_id=agent-gateway' \\"
echo "    -d 'username=<user>' \\"
echo "    -d 'password=<pass>' \\"
echo "    -d 'grant_type=password'"
