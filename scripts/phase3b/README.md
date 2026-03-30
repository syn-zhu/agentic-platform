# Phase 3b: Observability Stack

Installs the VictoriaMetrics metrics pipeline (VMSingle, VMAuth, vmagent), Grafana, and Kiali across all clusters via CAAPH (Cluster API Add-on Provider for Helm). Establishes a single-pane-of-glass metrics view with per-cluster labels and TLS-secured cross-cluster ingestion through an internal AWS NLB.

**Clusters:** `agentic-mgmt` (management), `agentic-obs` (observability), `agentic-cp`, `agentic-cell-1`, `agentic-cell-2`

---

## Prerequisites

- Phase 2 complete (5 clusters running, Gateway API CRDs on all, VPC TGW attached)
- Phase 3a substantially complete (control-plane services running)
- CAAPH installed on management cluster (`00-install-caaph.sh` from Task 1)
- Clusters labeled for CAAPH matching (`01-label-clusters.sh` from Task 2)
- `kubectl` contexts configured: `agentic-mgmt`, `agentic-obs`, `agentic-cp`, `agentic-cell-1`, `agentic-cell-2`
- AWS IRSA roles for AWS LB Controller on obs cluster (same pattern as control-plane)
- Tools: `kubectl`, `aws`, `curl`, `python3`, `base64`

---

## Run Order

```bash
cd ~/agentic-platform/scripts/phase3b

# 1. Install CAAPH on management cluster (~2 min)
./00-install-caaph.sh

# 2. Label clusters for CAAPH matching + create obs namespaces (~30 sec)
./01-label-clusters.sh

# 3. Apply HelmChartProxy CRs — triggers CAAPH installs (~10 min)
./02-apply-helmchartproxies.sh

# 4. Apply VM Operator CRs, NLB, distribute tokens (~5 min)
#    Run after vm-operator is running on agentic-obs
./03-apply-obs-manifests.sh

# 5. Configure Kiali remote secrets for cell clusters (~1 min)
./04-configure-kiali-remotes.sh

# 6. Verify the full stack
./05-verify.sh
```

---

## What Gets Created

### `02-apply-helmchartproxies.sh`

Applies all `platform/caaph/helmchartproxy-*.yaml` CRs to `agentic-mgmt`. CAAPH reconciles these into HelmReleaseProxy resources and installs the corresponding Helm releases onto the target clusters.

| HelmChartProxy | Target Cluster(s) | Selector |
|----------------|-------------------|----------|
| `cert-manager` | `agentic-obs` | `observability=true` |
| `aws-lb-controller` | `agentic-obs` | `observability=true` |
| `vm-operator` | `agentic-obs` | `observability=true` |
| `grafana` | `agentic-obs` | `observability=true` |
| `kiali-operator` | `agentic-obs` | `observability=true` |
| `vmagent` | all 4 clusters | `monitoring=vmagent` |

### `03-apply-obs-manifests.sh`

Applies `platform/observability-manifests/` to `agentic-obs` after the VM Operator CRDs are available. Creates:

| Resource | Namespace | Description |
|----------|-----------|-------------|
| `VMSingle/vm` | `monitoring` | VictoriaMetrics storage (50Gi gp3 PVC, 15d retention) |
| `VMAuth/vmauth` | `monitoring` | TLS-terminating auth proxy (self-signed cert from cert-manager) |
| `Service/vmauth-external` | `monitoring` | Internal AWS NLB for cross-cluster metric ingestion |
| `VMUser/cluster-agentic-*` | `monitoring` | Per-cluster write tokens (4 total, passwords auto-generated) |
| `Certificate/vmauth-tls-cert` | `monitoring` | cert-manager Certificate for VMAuth TLS |

Also extracts the generated VMUser bearer tokens and distributes them as `Secret/vmagent-remote-write-token` in each cluster's `monitoring` namespace. Updates `helmchartproxy-vmagent.yaml` with the actual NLB hostname and re-applies to trigger vmagent reconfiguration.

### `04-configure-kiali-remotes.sh`

For each cell cluster (`agentic-cell-1`, `agentic-cell-2`):

1. Creates `ServiceAccount/kiali-remote` in `kiali-operator` namespace on the cell cluster
2. Creates `ClusterRole/kiali-remote-viewer` (read-only: pods, services, Istio CRDs, Gateway API CRDs)
3. Creates `ClusterRoleBinding/kiali-remote-viewer` binding the SA to the role
4. Creates `Secret/kiali-remote-token` (type `kubernetes.io/service-account-token`) for a long-lived token
5. Extracts the token, CA cert, and API server URL
6. Creates `Secret/kiali-remote-<cluster>` in `istio-system` on `agentic-obs` containing a kubeconfig for the cell, labeled `kiali.io/multiCluster=true`

---

## Verification

Run `./05-verify.sh` for automated checks. Manual spot-checks:

```bash
# CAAPH — check HelmReleaseProxy status
kubectl --context agentic-mgmt get helmreleaseproxy -A

# VM Operator — check all VM CRDs are installed
kubectl --context agentic-obs get crd | grep victoriametrics

# VMSingle — check storage and status
kubectl --context agentic-obs -n monitoring get vmsingle vm -o yaml
kubectl --context agentic-obs -n monitoring get pvc

# VMAuth — check TLS cert and NLB
kubectl --context agentic-obs -n monitoring get vmauth vmauth -o yaml
kubectl --context agentic-obs -n monitoring get svc vmauth-external

# NLB DNS name
kubectl --context agentic-obs -n monitoring \
  get svc vmauth-external -o jsonpath='{.status.loadBalancer.ingress[0].hostname}'

# VMUser tokens (generated passwords)
kubectl --context agentic-obs -n monitoring get secrets | grep vmuser

# vmagent on a cell cluster
kubectl --context agentic-cell-1 -n monitoring get pods
kubectl --context agentic-cell-1 -n monitoring get secret vmagent-remote-write-token

# Grafana (port-forward)
kubectl --context agentic-obs port-forward -n monitoring svc/grafana 13000:80
# Open: http://localhost:13000 (admin/admin)

# Kiali (port-forward)
kubectl --context agentic-obs port-forward -n istio-system svc/kiali 20001:20001
# Open: http://localhost:20001

# Query VictoriaMetrics for metrics by cluster
kubectl --context agentic-obs port-forward -n monitoring svc/vmsingle-vm 9429:8429 &
curl -s 'http://localhost:9429/api/v1/query?query=count(up{})%20by%20(cluster)' | python3 -m json.tool
```

---

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│ agentic-mgmt (management cluster)                        │
│   CAAPH controller                                       │
│   HelmChartProxy CRs ──► reconciles ──► HelmReleaseProxy│
└──────────────────────────┬──────────────────────────────┘
                           │ installs Helm charts
              ┌────────────┼─────────────────────────────┐
              │            │                             │
    ┌─────────▼─────┐  ┌───▼──────────────────────────┐  │
    │ agentic-cp    │  │ agentic-obs                   │  │
    │ vmagent       │  │ cert-manager                  │  │
    │   │           │  │ aws-lb-controller             │  │
    └───┼───────────┘  │ vm-operator                   │  │
        │              │   VMSingle (storage)           │  │
    ┌───┼───────────┐  │   VMAuth ──► NLB (internal)   │  │
    │ agentic-      │  │   VMUser (per-cluster tokens)  │  │
    │ cell-1/2      │  │ grafana ──► VMSingle           │  │
    │ vmagent       │  │ kiali ──► cell kubeconfigs     │  │
    │   │           │  └──────────────────┬────────────┘  │
    └───┼───────────┘                     │               │
        │                                 │               │
        └─────────────────────────────────┘               │
              remote-write (bearer token)                  │
              ──► VMAuth NLB ──► VMSingle                 │
                                                          │
└─────────────────────────────────────────────────────────┘
```

---

## Cleanup

```bash
# Remove VMAuth NLB (to avoid AWS charges)
kubectl --context agentic-obs -n monitoring delete svc vmauth-external

# Remove VM Operator CRs
kubectl --context agentic-obs delete -f platform/observability-manifests/

# Remove HelmChartProxy CRs (stops CAAPH from managing charts)
kubectl --context agentic-mgmt delete -f platform/caaph/

# Remove Kiali remote secrets
kubectl --context agentic-obs -n istio-system delete secret \
  kiali-remote-agentic-cell-1 kiali-remote-agentic-cell-2

# Remove kiali-remote SAs from cell clusters
for CELL in agentic-cell-1 agentic-cell-2; do
  kubectl --context $CELL delete sa kiali-remote -n kiali-operator
  kubectl --context $CELL delete clusterrole kiali-remote-viewer
  kubectl --context $CELL delete clusterrolebinding kiali-remote-viewer
  kubectl --context $CELL delete secret kiali-remote-token -n kiali-operator
done
```
