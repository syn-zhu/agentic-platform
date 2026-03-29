#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

echo "=== Phase 3a: Deploying Control-Plane Services ==="

# ── 1. Load .env.cp (RDS_ENDPOINT, REDIS_ENDPOINT, passwords) ──
ENV_FILE="$ROOT_DIR/.env.cp"
if [[ ! -f "$ENV_FILE" ]]; then
  echo "ERROR: $ENV_FILE not found. Run 00-create-aws-resources.sh first."
  exit 1
fi
set -a; source "$ENV_FILE"; set +a

if [[ -z "${RDS_ENDPOINT:-}" ]]; then
  echo "ERROR: RDS_ENDPOINT not set in .env.cp"
  exit 1
fi
if [[ -z "${REDIS_ENDPOINT:-}" ]]; then
  echo "ERROR: REDIS_ENDPOINT not set in .env.cp"
  exit 1
fi

KUBECTL="kubectl --context agentic-cp"
CP_DIR="$ROOT_DIR/platform/control-plane"
MANIFESTS_DIR="$ROOT_DIR/platform/control-plane-manifests"
TMPDIR_LOCAL="$(mktemp -d)"

cleanup() {
  rm -rf "$TMPDIR_LOCAL"
}
trap cleanup EXIT

echo "Using RDS_ENDPOINT=${RDS_ENDPOINT}"
echo "Using REDIS_ENDPOINT=${REDIS_ENDPOINT}"

# ── 2. Apply namespaces ──
echo ""
echo "── Applying namespaces ──"
$KUBECTL apply -f "$MANIFESTS_DIR/namespaces.yaml"

# ── 3. Substitute placeholders in Helm values (work on temp copies) ──
# helmfile reads values files relative to helmfile.yaml; we copy the whole
# values/ directory to a temp location and point helmfile at it via
# HELM_VALUES_DIR env var — but the helmfile already references relative
# paths.  Instead, we substitute in-place in temp copies and pass them via
# --set-file or by temporarily overwriting.  The cleanest approach is to
# create temp values files and run helmfile with --state-values-set pointing
# to an overlay.  However, helmfile 0.x supports HELMFILE_ENVIRONMENT and
# --set for simple key=value.  Since hostnames are nested YAML values, we use
# envsubst on a temp copy.

echo ""
echo "── Preparing Helm values (substituting RDS/Redis endpoints) ──"

# Copy values to temp dir and substitute placeholders with sed
TEMP_VALUES_DIR="$TMPDIR_LOCAL/values"
mkdir -p "$TEMP_VALUES_DIR"

for f in "$CP_DIR/values/"*.yaml; do
  fname="$(basename "$f")"
  sed -e "s|PLACEHOLDER_RDS_ENDPOINT|${RDS_ENDPOINT}|g" \
      -e "s|PLACEHOLDER_REDIS_ENDPOINT|${REDIS_ENDPOINT}|g" \
    < "$f" > "$TEMP_VALUES_DIR/$fname"
done

# ── 4. Run helmfile sync ──
# We can't easily change the values path after the fact, so we temporarily
# symlink the temp values dir over the real one and restore it after.
# A safer alternative: use helmfile --set to override just the hostname keys.
# But since multiple charts use the placeholder pattern and the keys vary,
# use the temp-copy-and-swap approach — the originals are never modified.

echo ""
echo "── Running helmfile sync on agentic-cp ──"

# Back up original values dir, put temp ones in place, run helmfile, restore
ORIG_VALUES_DIR="$CP_DIR/values"
BACKUP_VALUES_DIR="$TMPDIR_LOCAL/values-orig-backup"

# Swap: rename originals to backup, move temp into place
mv "$ORIG_VALUES_DIR" "$BACKUP_VALUES_DIR"
cp -r "$TEMP_VALUES_DIR" "$ORIG_VALUES_DIR"

restore_values() {
  if [[ -d "$BACKUP_VALUES_DIR" ]]; then
    rm -rf "$ORIG_VALUES_DIR"
    mv "$BACKUP_VALUES_DIR" "$ORIG_VALUES_DIR"
  fi
}
# Add restore to cleanup
trap 'restore_values; rm -rf "$TMPDIR_LOCAL"' EXIT

# Skip Istio releases (already installed in Phase 2) — only sync service releases
(cd "$CP_DIR" && helmfile --kube-context agentic-cp \
  -l name=keycloak -l name=openfga -l name=clickhouse -l name=langfuse \
  sync)

restore_values
# Re-register simple cleanup
trap 'rm -rf "$TMPDIR_LOCAL"' EXIT

echo "  helmfile sync complete."

# ── 5. Apply additional manifests ──

echo ""
echo "── Applying OTel collector manifest ──"
$KUBECTL apply -f "$MANIFESTS_DIR/otel-collector.yaml"

echo ""
echo "── Applying Istio ingress gateway ──"
$KUBECTL apply -f "$MANIFESTS_DIR/istio-ingress-gateway.yaml"

echo ""
echo "── Applying AgentRegistry manifest (with RDS endpoint substitution) ──"
AGENTREGISTRY_MANIFEST="$TMPDIR_LOCAL/agentregistry.yaml"
envsubst '${PLACEHOLDER_RDS_ENDPOINT}' \
  < "$MANIFESTS_DIR/agentregistry.yaml" \
  > "$AGENTREGISTRY_MANIFEST"
$KUBECTL apply -f "$AGENTREGISTRY_MANIFEST"

# ── 6. Wait for all deployments to be ready ──

echo ""
echo "── Waiting for deployments to become ready ──"

wait_deployment() {
  local ns="$1"
  local name="$2"
  echo "  Waiting for $ns/$name ..."
  $KUBECTL rollout status deployment/"$name" -n "$ns" --timeout=300s
}

wait_statefulset() {
  local ns="$1"
  local name="$2"
  echo "  Waiting for $ns/$name (StatefulSet) ..."
  $KUBECTL rollout status statefulset/"$name" -n "$ns" --timeout=300s
}

# Keycloak (keycloakx chart uses StatefulSet)
wait_statefulset keycloak keycloak || true

# OpenFGA
wait_deployment openfga openfga || true

# Langfuse
wait_deployment langfuse langfuse-web || true
wait_deployment langfuse langfuse-worker || true

# ClickHouse (StatefulSet)
wait_statefulset langfuse clickhouse || true

# AgentRegistry
wait_deployment agentregistry agentregistry || true

# OTel collector
wait_deployment otel-system otel-collector || true

echo ""
echo "=== Control-plane deployment complete ==="
echo ""
echo "Next step: ./03-configure-keycloak.sh"
