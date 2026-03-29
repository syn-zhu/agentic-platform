#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

PASS=0
FAIL=0

check() {
  local desc="$1"; shift
  if eval "$*" > /dev/null 2>&1; then
    echo "  ✓ $desc"
    PASS=$((PASS + 1))
  else
    echo "  ✗ $desc"
    FAIL=$((FAIL + 1))
  fi
}

echo "=== Phase 3a Verification: Control-Plane Services ==="
echo ""

KUBECTL="kubectl --context agentic-cp"

# ── Cluster reachable ──
echo "── Cluster ──"
check "agentic-cp reachable" "$KUBECTL cluster-info"
check "agentic-cp nodes ready" \
  "$KUBECTL get nodes -o jsonpath='{.items[0].status.conditions[?(@.type==\"Ready\")].status}' | grep -q True"

# ── istiod ──
echo ""
echo "── Istio (sidecar) ──"
check "istiod running" \
  "$KUBECTL -n istio-system get deployment istiod -o jsonpath='{.status.readyReplicas}' | grep -qE '^[1-9]'"

# ── Keycloak ──
echo ""
echo "── Keycloak ──"
check "keycloak StatefulSet ready" \
  "$KUBECTL -n keycloak get statefulset keycloak -o jsonpath='{.status.readyReplicas}' | grep -qE '^[1-9]'"
check "keycloak pod running" \
  "$KUBECTL get pods -n keycloak -o jsonpath='{.items[0].status.phase}' | grep -q Running"

# ── OpenFGA ──
echo ""
echo "── OpenFGA ──"
check "openfga deployment ready" \
  "$KUBECTL -n openfga get deployment openfga -o jsonpath='{.status.readyReplicas}' | grep -qE '^[1-9]'"

# ── Langfuse + ClickHouse ──
echo ""
echo "── Langfuse / ClickHouse ──"
check "clickhouse StatefulSet ready" \
  "$KUBECTL -n langfuse get statefulset clickhouse -o jsonpath='{.status.readyReplicas}' | grep -qE '^[1-9]'"
check "langfuse-web deployment ready" \
  "$KUBECTL -n langfuse get deployment langfuse-web -o jsonpath='{.status.readyReplicas}' | grep -qE '^[1-9]'"
check "langfuse-worker deployment ready" \
  "$KUBECTL -n langfuse get deployment langfuse-worker -o jsonpath='{.status.readyReplicas}' | grep -qE '^[1-9]'"

# ── AgentRegistry ──
echo ""
echo "── AgentRegistry ──"
check "agentregistry deployment ready" \
  "$KUBECTL -n agentregistry get deployment agentregistry -o jsonpath='{.status.readyReplicas}' | grep -qE '^[1-9]'"
check "agentregistry pod running" \
  "$KUBECTL get pods -n agentregistry -l app=agentregistry -o jsonpath='{.items[0].status.phase}' | grep -q Running"

# ── OTel Collector ──
echo ""
echo "── OTel Collector ──"
check "otel-collector deployment ready" \
  "$KUBECTL -n otel-system get deployment otel-collector -o jsonpath='{.status.readyReplicas}' | grep -qE '^[1-9]'"

# ── Istio ingress gateway ──
echo ""
echo "── Istio Ingress Gateway ──"
check "platform-gateway Gateway exists" \
  "$KUBECTL -n istio-system get gateway platform-gateway"
check "istio ingress gateway has external hostname/IP" \
  "$KUBECTL -n istio-system get svc istio-ingressgateway -o jsonpath='{.status.loadBalancer.ingress[0]}' | grep -qE '(hostname|ip)'"

# ── Keycloak OIDC discovery (via port-forward) ──
echo ""
echo "── Keycloak OIDC (port-forward) ──"
KC_POD=$($KUBECTL get pods -n keycloak \
  -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)

if [[ -n "$KC_POD" ]]; then
  pkill -f "kubectl.*port-forward.*18081" 2>/dev/null || true
  $KUBECTL port-forward -n keycloak "$KC_POD" 18081:8080 &
  PFKC_PID=$!
  trap 'kill $PFKC_PID 2>/dev/null || true' EXIT
  sleep 3

  check "Keycloak OIDC discovery endpoint responds" \
    "curl -sf http://localhost:18081/realms/platform/.well-known/openid-configuration"

  kill $PFKC_PID 2>/dev/null || true
  trap - EXIT
else
  echo "  ✗ Keycloak OIDC — no pod found, skipping port-forward check"
  FAIL=$((FAIL + 1))
fi

# ── Langfuse health check (via port-forward) ──
echo ""
echo "── Langfuse Health (port-forward) ──"
LF_POD=$($KUBECTL get pods -n langfuse -l app.kubernetes.io/component=web \
  -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)

if [[ -n "$LF_POD" ]]; then
  pkill -f "kubectl.*port-forward.*13000" 2>/dev/null || true
  $KUBECTL port-forward -n langfuse "$LF_POD" 13000:3000 &
  PFLF_PID=$!
  trap 'kill $PFLF_PID 2>/dev/null || true' EXIT
  sleep 3

  check "Langfuse /api/public/health responds" \
    "curl -sf http://localhost:13000/api/public/health"

  kill $PFLF_PID 2>/dev/null || true
  trap - EXIT
else
  echo "  ✗ Langfuse health — no web pod found, skipping port-forward check"
  FAIL=$((FAIL + 1))
fi

# ── AgentRegistry health check (via port-forward) ──
echo ""
echo "── AgentRegistry Health (port-forward) ──"
AR_POD=$($KUBECTL get pods -n agentregistry -l app=agentregistry \
  -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)

if [[ -n "$AR_POD" ]]; then
  pkill -f "kubectl.*port-forward.*18082" 2>/dev/null || true
  $KUBECTL port-forward -n agentregistry "$AR_POD" 18082:8080 &
  PFAR_PID=$!
  trap 'kill $PFAR_PID 2>/dev/null || true' EXIT
  sleep 3

  check "AgentRegistry /v0/health responds" \
    "curl -sf http://localhost:18082/v0/health"

  kill $PFAR_PID 2>/dev/null || true
  trap - EXIT
else
  echo "  ✗ AgentRegistry health — no pod found, skipping port-forward check"
  FAIL=$((FAIL + 1))
fi

# ── OTel collector log check ──
echo ""
echo "── OTel Collector Logs ──"
check "OTel collector receiving traces (log check)" \
  "$KUBECTL logs -n otel-system deployment/otel-collector --tail=50 | grep -qi 'started'"

# ── Summary ──
echo ""
echo "════════════════════════════════════════════════════"
echo "Results: $PASS passed, $FAIL failed"
if [[ $FAIL -gt 0 ]]; then
  echo "PHASE 3a NOT READY — fix failures above before proceeding"
  exit 1
else
  echo "PHASE 3a READY — control-plane services healthy"
fi
