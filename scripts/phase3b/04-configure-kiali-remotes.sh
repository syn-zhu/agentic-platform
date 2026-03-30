#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

MGMT_CTX="agentic-mgmt"
OBS_CTX="agentic-obs"
CELL_CLUSTERS=(agentic-cell-1 agentic-cell-2)

echo "=== Phase 3b: Configure Kiali Remote Secrets ==="
echo ""

# ── Ensure kiali namespace exists on obs cluster ──
echo "Ensuring kiali remote secrets namespace on $OBS_CTX..."
kubectl --context "$OBS_CTX" create namespace istio-system \
  --dry-run=client -o yaml | kubectl --context "$OBS_CTX" apply -f - > /dev/null 2>&1
echo "  ✓ istio-system namespace ready on $OBS_CTX"
echo ""

for CELL in "${CELL_CLUSTERS[@]}"; do
  echo "── Configuring Kiali remote access for $CELL ──"

  # ── Step 1: Retrieve cell cluster kubeconfig from management cluster ──
  # CAPI generates a kubeconfig Secret named <cluster>-kubeconfig in the default namespace
  echo "  Retrieving kubeconfig for $CELL from management cluster..."
  CELL_KUBECONFIG_B64=$(kubectl --context "$MGMT_CTX" -n default \
    get secret "${CELL}-kubeconfig" \
    -o jsonpath='{.data.value}' 2>/dev/null || echo "")

  if [[ -z "$CELL_KUBECONFIG_B64" ]]; then
    echo "  ERROR: kubeconfig secret '${CELL}-kubeconfig' not found on management cluster."
    echo "  Ensure CAPI has provisioned the cluster. Check:"
    echo "    kubectl --context $MGMT_CTX -n default get secret ${CELL}-kubeconfig"
    continue
  fi

  # Write kubeconfig to a temp file for cell cluster operations
  CELL_KF=$(mktemp /tmp/kiali-cell-kubeconfig.XXXXXX)
  echo "$CELL_KUBECONFIG_B64" | base64 -d > "$CELL_KF"
  trap 'rm -f "$CELL_KF"' EXIT

  echo "  ✓ kubeconfig retrieved for $CELL"

  # ── Step 2: Create kiali-remote ServiceAccount on cell cluster ──
  echo "  Creating kiali-remote ServiceAccount on $CELL..."
  kubectl --context "$CELL" create namespace kiali-operator \
    --dry-run=client -o yaml | kubectl --context "$CELL" apply -f - > /dev/null 2>&1

  kubectl --context "$CELL" apply -f - <<EOF
apiVersion: v1
kind: ServiceAccount
metadata:
  name: kiali-remote
  namespace: kiali-operator
EOF

  echo "  ✓ ServiceAccount kiali-remote created in kiali-operator namespace"

  # ── Step 3: Create read-only ClusterRoleBinding on cell cluster ──
  echo "  Creating ClusterRoleBinding for kiali-remote on $CELL..."
  kubectl --context "$CELL" apply -f - <<EOF
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kiali-remote-viewer
rules:
  - apiGroups: [""]
    resources:
      - configmaps
      - endpoints
      - namespaces
      - nodes
      - pods
      - pods/log
      - replicationcontrollers
      - services
    verbs: ["get", "list", "watch"]
  - apiGroups: ["extensions", "apps"]
    resources:
      - deployments
      - replicasets
      - statefulsets
      - daemonsets
    verbs: ["get", "list", "watch"]
  - apiGroups: ["batch"]
    resources: ["cronjobs", "jobs"]
    verbs: ["get", "list", "watch"]
  - apiGroups: ["networking.istio.io", "security.istio.io", "gateway.networking.k8s.io"]
    resources: ["*"]
    verbs: ["get", "list", "watch"]
  - apiGroups: ["authentication.istio.io", "config.istio.io", "telemetry.istio.io"]
    resources: ["*"]
    verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: kiali-remote-viewer
subjects:
  - kind: ServiceAccount
    name: kiali-remote
    namespace: kiali-operator
roleRef:
  kind: ClusterRole
  name: kiali-remote-viewer
  apiGroup: rbac.authorization.k8s.io
EOF

  echo "  ✓ ClusterRoleBinding kiali-remote-viewer created on $CELL"

  # ── Step 4: Create a long-lived token Secret for the SA ──
  echo "  Creating long-lived token Secret for kiali-remote on $CELL..."
  kubectl --context "$CELL" apply -f - <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: kiali-remote-token
  namespace: kiali-operator
  annotations:
    kubernetes.io/service-account.name: kiali-remote
type: kubernetes.io/service-account-token
EOF

  # Wait for the token to be populated by the token controller
  echo "  Waiting for token to be populated..."
  DEADLINE=$((SECONDS + 60))
  while true; do
    TOKEN_DATA=$(kubectl --context "$CELL" -n kiali-operator \
      get secret kiali-remote-token \
      -o jsonpath='{.data.token}' 2>/dev/null || echo "")
    if [[ -n "$TOKEN_DATA" ]]; then
      break
    fi
    if [[ $SECONDS -ge $DEADLINE ]]; then
      echo "  ERROR: Token not populated after 60s for $CELL. Check:"
      echo "    kubectl --context $CELL -n kiali-operator get secret kiali-remote-token -o yaml"
      continue 2
    fi
    sleep 5
  done

  echo "  ✓ Token populated"

  # ── Step 5: Extract token and CA cert ──
  TOKEN=$(echo "$TOKEN_DATA" | base64 -d)
  CA_CERT=$(kubectl --context "$CELL" -n kiali-operator \
    get secret kiali-remote-token \
    -o jsonpath='{.data.ca\.crt}' 2>/dev/null || echo "")
  API_SERVER=$(kubectl --context "$CELL" cluster-info 2>/dev/null \
    | grep -m1 "Kubernetes control plane" | awk '{print $NF}' \
    | sed 's/\x1b\[[0-9;]*m//g' || echo "")

  if [[ -z "$TOKEN" || -z "$CA_CERT" || -z "$API_SERVER" ]]; then
    echo "  ERROR: Could not extract token, CA cert, or API server URL for $CELL."
    echo "    API_SERVER=$API_SERVER"
    echo "    TOKEN length: ${#TOKEN}"
    echo "    CA_CERT length: ${#CA_CERT}"
    continue
  fi

  echo "  ✓ API server: $API_SERVER"
  echo "  ✓ Token extracted (${#TOKEN} chars)"
  echo "  ✓ CA cert extracted"

  # ── Step 6: Build kubeconfig for Kiali remote secret ──
  KIALI_KUBECONFIG=$(cat <<KUBECFG
apiVersion: v1
kind: Config
clusters:
  - name: ${CELL}
    cluster:
      server: ${API_SERVER}
      certificate-authority-data: ${CA_CERT}
contexts:
  - name: ${CELL}
    context:
      cluster: ${CELL}
      user: kiali-remote
current-context: ${CELL}
users:
  - name: kiali-remote
    user:
      token: ${TOKEN}
KUBECFG
)

  # ── Step 7: Create Kiali remote secret on the obs cluster ──
  # Kiali discovers remote cluster secrets labeled with kiali.io/multiCluster: "true"
  # in the Kiali deployment namespace (istio-system).
  echo "  Creating Kiali remote secret on $OBS_CTX for $CELL..."
  kubectl --context "$OBS_CTX" create secret generic \
    "kiali-remote-${CELL}" \
    --namespace istio-system \
    --from-literal=kubeconfig="$KIALI_KUBECONFIG" \
    --dry-run=client -o yaml \
  | kubectl --context "$OBS_CTX" apply -f -

  # Label the secret for Kiali multi-cluster discovery
  kubectl --context "$OBS_CTX" -n istio-system label secret \
    "kiali-remote-${CELL}" \
    "kiali.io/multiCluster=true" \
    --overwrite

  echo "  ✓ Kiali remote secret 'kiali-remote-${CELL}' created on $OBS_CTX (labeled kiali.io/multiCluster=true)"
  echo ""

  # Clean up temp kubeconfig
  rm -f "$CELL_KF"
  trap - EXIT
done

echo ""
echo "=== Kiali remote secrets configured ==="
echo ""
echo "Verify on the obs cluster:"
for CELL in "${CELL_CLUSTERS[@]}"; do
  echo "  kubectl --context $OBS_CTX -n istio-system get secret kiali-remote-${CELL} -o yaml"
done
echo ""
echo "Kiali should now show ${#CELL_CLUSTERS[@]} remote clusters in the multi-cluster view."
echo "Next: run 05-verify.sh"
