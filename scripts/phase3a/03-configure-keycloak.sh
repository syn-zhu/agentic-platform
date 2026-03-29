#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

echo "=== Phase 3a: Configuring Keycloak (platform realm) ==="

# ── Load .env.cp for KEYCLOAK_ADMIN_PASSWORD ──
ENV_FILE="$ROOT_DIR/.env.cp"
if [[ ! -f "$ENV_FILE" ]]; then
  echo "ERROR: $ENV_FILE not found. Run 00-create-aws-resources.sh first."
  exit 1
fi
set -a; source "$ENV_FILE"; set +a

if [[ -z "${KEYCLOAK_ADMIN_PASSWORD:-}" ]]; then
  echo "ERROR: KEYCLOAK_ADMIN_PASSWORD not set in .env.cp"
  exit 1
fi

REALM_FILE="$ROOT_DIR/platform/control-plane-manifests/keycloak-platform-realm.json"
if [[ ! -f "$REALM_FILE" ]]; then
  echo "ERROR: Realm file not found at $REALM_FILE"
  exit 1
fi

KUBECTL="kubectl --context agentic-cp"
ADMIN_USER="admin"
ADMIN_PASS="${KEYCLOAK_ADMIN_PASSWORD}"
PF_PORT=18080
KEYCLOAK_URL="http://localhost:${PF_PORT}"

# ── Start port-forward to Keycloak ──
echo "Starting port-forward to Keycloak (agentic-cp, namespace keycloak, port ${PF_PORT})..."

# Find the keycloak pod
KC_POD=$($KUBECTL get pods -n keycloak -l app.kubernetes.io/name=keycloak \
  -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)

if [[ -z "$KC_POD" ]]; then
  # keycloakx chart may label pods differently
  KC_POD=$($KUBECTL get pods -n keycloak \
    -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
fi

if [[ -z "$KC_POD" ]]; then
  echo "ERROR: No Keycloak pod found in namespace 'keycloak' on agentic-cp."
  echo "  Ensure helmfile sync completed and the pod is running."
  exit 1
fi

echo "  Found pod: $KC_POD"

# Kill any existing port-forward on that port
pkill -f "kubectl.*port-forward.*${PF_PORT}" 2>/dev/null || true

$KUBECTL port-forward -n keycloak "$KC_POD" "${PF_PORT}:8080" &
PF_PID=$!
trap 'kill $PF_PID 2>/dev/null || true' EXIT

# Wait for port-forward to be ready
echo "  Waiting for port-forward to be ready..."
for i in $(seq 1 20); do
  if curl -s -o /dev/null "${KEYCLOAK_URL}/realms/master" 2>/dev/null; then
    break
  fi
  sleep 1
done

# ── Authenticate to Keycloak admin API ──
echo "Authenticating to Keycloak admin API..."

ADMIN_TOKEN=$(curl -s -X POST \
  "${KEYCLOAK_URL}/realms/master/protocol/openid-connect/token" \
  -d "client_id=admin-cli" \
  -d "username=${ADMIN_USER}" \
  -d "password=${ADMIN_PASS}" \
  -d "grant_type=password" \
  | python3 -c "import sys,json; print(json.load(sys.stdin).get('access_token',''))" 2>/dev/null)

if [[ -z "$ADMIN_TOKEN" ]]; then
  echo "ERROR: Failed to get admin token from Keycloak."
  echo "  Verify Keycloak is running and KEYCLOAK_ADMIN_PASSWORD is correct."
  exit 1
fi
echo "  Authenticated."

kc_api() {
  # kc_api METHOD PATH [body]
  local method="$1"
  local path="$2"
  local body="${3:-}"
  if [[ -n "$body" ]]; then
    curl -s -X "$method" \
      -H "Authorization: Bearer ${ADMIN_TOKEN}" \
      -H "Content-Type: application/json" \
      -d "$body" \
      "${KEYCLOAK_URL}${path}"
  else
    curl -s -X "$method" \
      -H "Authorization: Bearer ${ADMIN_TOKEN}" \
      "${KEYCLOAK_URL}${path}"
  fi
}

kc_status() {
  local path="$1"
  curl -s -o /dev/null -w "%{http_code}" \
    -H "Authorization: Bearer ${ADMIN_TOKEN}" \
    "${KEYCLOAK_URL}${path}"
}

# ── Create or update the platform realm ──
echo ""
echo "── Importing 'platform' realm ──"

REALM_EXISTS=$(kc_status "/admin/realms/platform" || echo "000")

if [[ "$REALM_EXISTS" == "200" ]]; then
  echo "  Realm 'platform' already exists — running partial import to sync clients/scopes..."
  IMPORT_RESPONSE=$(curl -s -X POST \
    -H "Authorization: Bearer ${ADMIN_TOKEN}" \
    -H "Content-Type: application/json" \
    -d @"$REALM_FILE" \
    "${KEYCLOAK_URL}/admin/realms/platform/partialImport")
  echo "  Partial import result: $(echo "$IMPORT_RESPONSE" | python3 -c "import sys,json; d=json.load(sys.stdin); print(f'added={d.get(\"added\",0)}, skipped={d.get(\"skipped\",0)}, overwritten={d.get(\"overwritten\",0)}')" 2>/dev/null || echo "$IMPORT_RESPONSE")"
else
  echo "  Creating new 'platform' realm..."
  HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST \
    -H "Authorization: Bearer ${ADMIN_TOKEN}" \
    -H "Content-Type: application/json" \
    -d @"$REALM_FILE" \
    "${KEYCLOAK_URL}/admin/realms")
  if [[ "$HTTP_CODE" == "201" ]]; then
    echo "  Realm 'platform' created."
  else
    echo "  WARNING: Realm creation returned HTTP ${HTTP_CODE}. May have partially succeeded."
  fi
fi

# ── Enable Organizations on the realm (idempotent PUT) ──
echo ""
echo "── Enabling Organizations on 'platform' realm ──"
kc_api PUT "/admin/realms/platform" \
  '{"organizationsEnabled": true}' > /dev/null
echo "  Organizations enabled."

# ── Ensure 'organization' client scope exists ──
# The realm JSON includes it, but on some KC versions partial import doesn't
# create it.  Check and create manually if missing.
echo ""
echo "── Ensuring 'organization' client scope exists ──"

EXISTING_SCOPES=$(kc_api GET "/admin/realms/platform/client-scopes" || echo "[]")
ORG_SCOPE_ID=$(echo "$EXISTING_SCOPES" \
  | python3 -c "import sys,json; scopes=json.load(sys.stdin); matches=[s['id'] for s in scopes if s['name']=='organization']; print(matches[0] if matches else '')" 2>/dev/null)

if [[ -z "$ORG_SCOPE_ID" ]]; then
  echo "  'organization' scope not found — creating..."
  ORG_SCOPE_JSON='{
    "name": "organization",
    "description": "Keycloak Organizations membership claim",
    "protocol": "openid-connect",
    "attributes": {
      "include.in.token.scope": "true",
      "display.on.consent.screen": "false"
    },
    "protocolMappers": [
      {
        "name": "organization",
        "protocol": "openid-connect",
        "protocolMapper": "oidc-organization-membership-mapper",
        "consentRequired": false,
        "config": {
          "introspection.token.claim": "true",
          "userinfo.token.claim": "true",
          "id.token.claim": "true",
          "access.token.claim": "true"
        }
      }
    ]
  }'
  SC_CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST \
    -H "Authorization: Bearer ${ADMIN_TOKEN}" \
    -H "Content-Type: application/json" \
    -d "$ORG_SCOPE_JSON" \
    "${KEYCLOAK_URL}/admin/realms/platform/client-scopes")
  echo "  Create scope HTTP status: $SC_CODE"

  # Re-fetch ID
  EXISTING_SCOPES=$(kc_api GET "/admin/realms/platform/client-scopes" || echo "[]")
  ORG_SCOPE_ID=$(echo "$EXISTING_SCOPES" \
    | python3 -c "import sys,json; scopes=json.load(sys.stdin); matches=[s['id'] for s in scopes if s['name']=='organization']; print(matches[0] if matches else '')" 2>/dev/null)
else
  echo "  'organization' scope already exists (id: ${ORG_SCOPE_ID})."
fi

# ── Add 'organization' to the default client scopes ──
if [[ -n "$ORG_SCOPE_ID" ]]; then
  echo "  Adding 'organization' to default client scopes..."
  curl -s -o /dev/null -X PUT \
    -H "Authorization: Bearer ${ADMIN_TOKEN}" \
    "${KEYCLOAK_URL}/admin/realms/platform/default-default-client-scopes/${ORG_SCOPE_ID}" || true
  echo "  Done."
fi

# ── Configure K8s OIDC identity provider ──
echo ""
echo "── Configuring K8s OIDC identity provider ──"

K8S_OIDC_ISSUER=$($KUBECTL get --raw /.well-known/openid-configuration \
  | python3 -c "import sys,json; print(json.load(sys.stdin)['issuer'])" 2>/dev/null || true)

if [[ -z "$K8S_OIDC_ISSUER" ]]; then
  echo "  WARNING: Could not determine K8s OIDC issuer. Skipping IdP configuration."
else
  IDP_EXISTS=$(kc_status "/admin/realms/platform/identity-provider/instances/kubernetes" || echo "000")

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
    echo "  K8s OIDC IdP exists — updating..."
    kc_api PUT "/admin/realms/platform/identity-provider/instances/kubernetes" "$IDP_JSON" > /dev/null
  else
    echo "  Creating K8s OIDC identity provider..."
    kc_api POST "/admin/realms/platform/identity-provider/instances" "$IDP_JSON" > /dev/null
  fi
  echo "  OK — K8s OIDC IdP configured (issuer: ${K8S_OIDC_ISSUER})."
fi

# ── Create initial org 'acme' for testing ──
echo ""
echo "── Creating initial org 'acme' ──"

ORG_LIST=$(kc_api GET "/admin/realms/platform/organizations" || echo "[]")
ACME_EXISTS=$(echo "$ORG_LIST" \
  | python3 -c "import sys,json; orgs=json.load(sys.stdin); print('yes' if any(o.get('name')=='acme' for o in orgs) else 'no')" 2>/dev/null || echo "no")

if [[ "$ACME_EXISTS" == "yes" ]]; then
  echo "  Org 'acme' already exists — skipping."
else
  ORG_JSON='{"name":"acme","domains":[{"name":"acme.example.com","verified":false}]}'
  ORG_CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST \
    -H "Authorization: Bearer ${ADMIN_TOKEN}" \
    -H "Content-Type: application/json" \
    -d "$ORG_JSON" \
    "${KEYCLOAK_URL}/admin/realms/platform/organizations")
  if [[ "$ORG_CODE" == "201" ]]; then
    echo "  Org 'acme' created."
  else
    echo "  WARNING: Org creation returned HTTP ${ORG_CODE}."
  fi
fi

# ── Create Initial Access Token for DCR ──
echo ""
echo "── Creating Initial Access Token for DCR ──"

$KUBECTL create namespace platform-system --dry-run=client -o yaml | $KUBECTL apply -f - 2>/dev/null

IAT_RESPONSE=$(kc_api POST "/admin/realms/platform/clients-initial-access" \
  '{"count": 100, "expiration": 0}')

IAT_TOKEN=$(echo "$IAT_RESPONSE" \
  | python3 -c "import sys,json; print(json.load(sys.stdin).get('token',''))" 2>/dev/null)

if [[ -z "$IAT_TOKEN" ]]; then
  echo "  WARNING: Failed to create IAT. DCR will not be available."
  echo "  Response: ${IAT_RESPONSE}"
else
  $KUBECTL create secret generic keycloak-initial-access-token \
    --namespace platform-system \
    --from-literal=token="$IAT_TOKEN" \
    --dry-run=client -o yaml | $KUBECTL apply -f -
  echo "  OK — IAT stored as Secret 'keycloak-initial-access-token' in platform-system."
fi

# ── Verify OIDC discovery ──
echo ""
echo "── Verifying OIDC discovery endpoint ──"
OIDC_STATUS=$(curl -s -o /dev/null -w "%{http_code}" \
  "${KEYCLOAK_URL}/realms/platform/.well-known/openid-configuration" || echo "000")

if [[ "$OIDC_STATUS" == "200" ]]; then
  echo "  OK — OIDC discovery at ${KEYCLOAK_URL}/realms/platform/.well-known/openid-configuration"
else
  echo "  FAIL — OIDC discovery returned HTTP ${OIDC_STATUS}"
fi

# ── Verify agentregistry client ──
echo ""
echo "── Verifying agentregistry client ──"
CLIENT_CHECK=$(kc_api GET "/admin/realms/platform/clients?clientId=agentregistry" \
  | python3 -c "import sys,json; clients=json.load(sys.stdin); print('found' if clients else 'missing')" 2>/dev/null || echo "error")

if [[ "$CLIENT_CHECK" == "found" ]]; then
  echo "  OK — agentregistry client exists in platform realm."
else
  echo "  FAIL — agentregistry client not found (status: ${CLIENT_CHECK}). Check realm import."
fi

# ── Summary ──
echo ""
echo "=== Keycloak configuration complete ==="
echo ""
echo "OIDC Issuer (via port-forward):  ${KEYCLOAK_URL}/realms/platform"
echo "OIDC Issuer (in-cluster):        http://keycloak.keycloak.svc.cluster.local:8080/realms/platform"
echo "Clients:                         agent-gateway, agentregistry"
echo "K8s OIDC IdP:                    ${K8S_OIDC_ISSUER:-not configured}"
echo "DCR IAT Secret:                  platform-system/keycloak-initial-access-token"
echo "Test org:                        acme"
echo ""
echo "Test token (password grant, requires a user in the platform realm):"
echo "  kubectl --context agentic-cp port-forward -n keycloak svc/keycloak 18080:8080"
echo "  curl -s -X POST http://localhost:18080/realms/platform/protocol/openid-connect/token \\"
echo "    -d 'client_id=agent-gateway' \\"
echo "    -d 'username=<user>' \\"
echo "    -d 'password=<pass>' \\"
echo "    -d 'grant_type=password'"
echo ""
echo "Next step: ./04-verify.sh"
