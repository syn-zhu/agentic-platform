#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

echo "=== Phase 7: Configuring OpenFGA ==="

# ══════════════════════════════════════════════════════════════════
# Prerequisites
# ══════════════════════════════════════════════════════════════════
# The OpenFGA server is deployed via Helm (helmfile sync in 03-deploy-platform.sh).
# This script builds and pushes the openfga-envoy image, deploys the ext_authz
# adapter, and verifies everything is healthy.
# It does NOT create stores or models -- those are created per-tenant during onboarding.

REGION="us-east-1"
AWS_ACCOUNT_ID=$(aws sts get-caller-identity --query 'Account' --output text)
ECR_REGISTRY="${AWS_ACCOUNT_ID}.dkr.ecr.${REGION}.amazonaws.com"
OPENFGA_ENVOY_IMAGE="${ECR_REGISTRY}/agentic-platform/openfga-envoy:latest"

# ── Wait for OpenFGA server (deployed by helmfile) ──
echo "Waiting for OpenFGA server to be ready..."
kubectl rollout status deployment/openfga -n openfga --timeout=120s

# ══════════════════════════════════════════════════════════════════
# Build and push openfga-envoy image
# ══════════════════════════════════════════════════════════════════
# openfga-envoy is vendored at vendor/openfga-envoy/ with patches for
# per-request store ID resolution via header. No upstream image is published.

echo ""
echo "Building openfga-envoy image..."
docker build -t "$OPENFGA_ENVOY_IMAGE" "$ROOT_DIR/vendor/openfga-envoy/extauthz"

echo "Logging in to ECR..."
aws ecr get-login-password --region "$REGION" | \
  docker login --username AWS --password-stdin "$ECR_REGISTRY"

echo "Pushing openfga-envoy image..."
docker push "$OPENFGA_ENVOY_IMAGE"
echo "  Pushed: $OPENFGA_ENVOY_IMAGE"

# ══════════════════════════════════════════════════════════════════
# Deploy openfga-envoy (ext_authz adapter)
# ══════════════════════════════════════════════════════════════════

echo ""
echo "Applying openfga-envoy manifests..."
kubectl apply -f "$ROOT_DIR/platform/manifests/openfga.yaml"

echo "Waiting for openfga-envoy to be ready..."
kubectl rollout status deployment/openfga-envoy -n openfga --timeout=120s

# ── Helper: run curl inside the cluster ──
openfga_curl() {
  kubectl run openfga-curl-$RANDOM --rm -i --restart=Never \
    --image=curlimages/curl:latest -n openfga \
    -- curl -s "$@" 2>/dev/null
}

# ── Verify OpenFGA health ──
echo ""
echo "Verifying OpenFGA server health..."
HEALTH_STATUS=$(openfga_curl -o /dev/null -w "%{http_code}" \
  "http://openfga.openfga.svc.cluster.local:8080/healthz" || echo "000")

if [[ "$HEALTH_STATUS" == "200" ]]; then
  echo "  OK -- OpenFGA server is healthy."
else
  echo "  FAIL -- OpenFGA health check returned HTTP ${HEALTH_STATUS}."
  echo "  Check: kubectl logs -n openfga deployment/openfga"
  exit 1
fi

# ── Verify openfga-envoy is responding to gRPC health checks ──
echo "Verifying openfga-envoy health..."
ENVOY_READY=$(kubectl get pods -n openfga -l app=openfga-envoy \
  -o jsonpath='{.items[0].status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || echo "Unknown")

if [[ "$ENVOY_READY" == "True" ]]; then
  echo "  OK -- openfga-envoy pod is ready (gRPC health check passing)."
else
  echo "  FAIL -- openfga-envoy pod is not ready (status: ${ENVOY_READY})."
  echo "  Check: kubectl logs -n openfga deployment/openfga-envoy"
  exit 1
fi

# ══════════════════════════════════════════════════════════════════
# Summary
# ══════════════════════════════════════════════════════════════════

echo ""
echo "=== OpenFGA configuration complete ==="
echo ""
echo "OpenFGA server:    openfga.openfga.svc.cluster.local:8080 (HTTP) / :8081 (gRPC)"
echo "openfga-envoy:     openfga-envoy.openfga.svc.cluster.local:9002 (ext_authz gRPC)"
echo "Playground:        openfga.openfga.svc.cluster.local:3000"
echo "Image:             $OPENFGA_ENVOY_IMAGE"
echo ""
echo "No stores or models have been created."
echo "Stores and models are created per-tenant during onboarding (05-onboard-tenant.sh)."
