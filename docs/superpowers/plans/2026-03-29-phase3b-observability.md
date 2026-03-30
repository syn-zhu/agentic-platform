# Phase 3b: Observability — CAAPH, VictoriaMetrics, Grafana, Kiali, vmagent

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Install CAAPH on the management cluster, then use it to declaratively deploy VictoriaMetrics + Grafana + Kiali to the observability cluster and vmagent to all clusters — so that metrics from every cluster flow into a single VictoriaMetrics instance with proper cluster labels.

**Architecture:** CAAPH (Cluster API Addon Provider for Helm) is installed on the management cluster alongside CAPA. `HelmChartProxy` CRs on the management cluster declare which Helm charts to install on which clusters (matched by labels). CAAPH uses CAPI-generated kubeconfig secrets to install charts into workload clusters. VictoriaMetrics and Grafana go to the obs cluster; vmagent goes to all clusters with per-cluster `externalLabels` via Go template. Kiali is deployed to the obs cluster with remote secrets for cell cluster API access.

**Tech Stack:** CAAPH (Helm), VictoriaMetrics, Grafana, Kiali, vmagent

**Spec:** `docs/superpowers/specs/2026-03-25-multi-cluster-architecture-design.md`
**Phase 3 scope:** Memory: `project_phase3_scope.md`

**Pre-requisites:**
- Phase 2 complete (5 clusters running)
- Phase 3a substantially complete (control-plane services running)
- Management cluster has CAPI Operator + CAPA
- `AWS_PROFILE=mms-test`, kubectl contexts for all clusters

---

## File Structure

```
platform/
  management-manifests/
    caaph-provider.yaml                  # AddonProvider CR for CAAPH
  caaph/
    helmchartproxy-vmagent.yaml          # vmagent on ALL clusters
    helmchartproxy-victoriametrics.yaml  # VictoriaMetrics on obs cluster
    helmchartproxy-grafana.yaml          # Grafana on obs cluster
    helmchartproxy-kiali.yaml            # Kiali on obs cluster
    values/
      vmagent.yaml                       # vmagent values template (Go template for cluster name)
      victoriametrics.yaml               # VM single-node values
      grafana.yaml                       # Grafana values (datasource: VM)
      kiali.yaml                         # Kiali values (multi-cluster)
  observability-manifests/
    namespaces.yaml                      # obs cluster namespaces
    kiali-remote-secrets.yaml            # Template for Kiali remote cluster access
scripts/phase3b/
  00-install-caaph.sh                    # Install CAAPH on management cluster
  01-label-clusters.sh                   # Add labels to Cluster CRs for CAAPH matching
  02-apply-helmchartproxies.sh           # Apply HelmChartProxy CRs
  03-configure-kiali-remotes.sh          # Create Kiali remote secrets on obs cluster
  04-verify.sh                           # Smoke tests
  README.md
```

---

### Task 1: Install CAAPH on the management cluster

**Files:**
- Create: `platform/management-manifests/caaph-provider.yaml`
- Create: `scripts/phase3b/00-install-caaph.sh`

- [ ] **Step 1: Create CAAPH AddonProvider manifest**

`platform/management-manifests/caaph-provider.yaml`:
```yaml
# CAAPH — Cluster API Addon Provider for Helm
# Installed via the CAPI Operator. Watches HelmChartProxy CRs and installs
# Helm charts into workload clusters matched by label selectors.
apiVersion: operator.cluster.x-k8s.io/v1alpha2
kind: AddonProvider
metadata:
  name: helm
  namespace: caaph-system
spec:
  version: v0.3.2
```

- [ ] **Step 2: Create install script**

`scripts/phase3b/00-install-caaph.sh` (chmod +x):
- Creates `caaph-system` namespace
- Applies the AddonProvider CR from the committed manifest
- Waits for the CAAPH controller to be ready
- Verifies HelmChartProxy CRD is available

- [ ] **Step 3: Commit**

```bash
git add platform/management-manifests/caaph-provider.yaml scripts/phase3b/00-install-caaph.sh
git commit -m "feat(mgmt): add CAAPH addon provider manifest and install script"
```

---

### Task 2: Label Cluster CRs for CAAPH matching

**Files:**
- Create: `scripts/phase3b/01-label-clusters.sh`

- [ ] **Step 1: Create label script**

The script adds labels to the Cluster CRs on the management cluster so HelmChartProxy selectors can match them:

```bash
# All clusters get vmagent
kubectl --context agentic-mgmt label cluster agentic-cp  -n default monitoring=vmagent --overwrite
kubectl --context agentic-mgmt label cluster agentic-obs  -n default monitoring=vmagent --overwrite
kubectl --context agentic-mgmt label cluster agentic-cell-1 -n default monitoring=vmagent --overwrite
kubectl --context agentic-mgmt label cluster agentic-cell-2 -n default monitoring=vmagent --overwrite

# Only obs cluster gets the observability stack
kubectl --context agentic-mgmt label cluster agentic-obs -n default observability=true --overwrite

# Cell clusters get kiali-remote label (for Kiali SA creation)
kubectl --context agentic-mgmt label cluster agentic-cell-1 -n default kiali-remote=true --overwrite
kubectl --context agentic-mgmt label cluster agentic-cell-2 -n default kiali-remote=true --overwrite
```

- [ ] **Step 2: Commit**

```bash
git add scripts/phase3b/01-label-clusters.sh
git commit -m "feat(mgmt): add cluster labeling script for CAAPH matching"
```

---

### Task 3: Create vmagent HelmChartProxy (all clusters)

**Files:**
- Create: `platform/caaph/helmchartproxy-vmagent.yaml`
- Create: `platform/caaph/values/vmagent.yaml`

- [ ] **Step 1: Create HelmChartProxy for vmagent**

`platform/caaph/helmchartproxy-vmagent.yaml`:
```yaml
apiVersion: addons.cluster.x-k8s.io/v1alpha1
kind: HelmChartProxy
metadata:
  name: vmagent
  namespace: default
spec:
  clusterSelector:
    matchLabels:
      monitoring: vmagent
  repoURL: https://victoriametrics.github.io/helm-charts
  chartName: victoria-metrics-agent
  releaseName: vmagent
  releaseNamespace: monitoring
  valuesTemplate: |
    # Per-cluster external labels — cluster name injected via Go template
    config:
      global:
        external_labels:
          cluster: "{{ .Cluster.metadata.name }}"
    remoteWriteUrls:
      - http://victoriametrics.monitoring.svc.cluster.local:8428/api/v1/write
```

Note: The `remoteWriteUrls` points to `victoriametrics.monitoring.svc.cluster.local` — this only resolves on the obs cluster. For other clusters, vmagent will need a cross-cluster URL. Since there's no cross-cluster DNS, we'll need to use the obs cluster's NLB or a known endpoint. This is a detail to work out during implementation — for now, vmagent on the obs cluster itself will work, and other clusters will need the TGW-routable endpoint.

- [ ] **Step 2: Commit**

```bash
git add platform/caaph/
git commit -m "feat(obs): add vmagent HelmChartProxy for all clusters via CAAPH"
```

---

### Task 4: Create VictoriaMetrics HelmChartProxy (obs cluster)

**Files:**
- Create: `platform/caaph/helmchartproxy-victoriametrics.yaml`

- [ ] **Step 1: Create HelmChartProxy**

```yaml
apiVersion: addons.cluster.x-k8s.io/v1alpha1
kind: HelmChartProxy
metadata:
  name: victoriametrics
  namespace: default
spec:
  clusterSelector:
    matchLabels:
      observability: "true"
  repoURL: https://victoriametrics.github.io/helm-charts
  chartName: victoria-metrics-single
  releaseName: victoriametrics
  releaseNamespace: monitoring
  valuesTemplate: |
    server:
      retentionPeriod: 15d
      persistentVolume:
        enabled: true
        size: 50Gi
        storageClass: gp3
```

- [ ] **Step 2: Commit**

```bash
git add platform/caaph/helmchartproxy-victoriametrics.yaml
git commit -m "feat(obs): add VictoriaMetrics HelmChartProxy for obs cluster"
```

---

### Task 5: Create Grafana HelmChartProxy (obs cluster)

**Files:**
- Create: `platform/caaph/helmchartproxy-grafana.yaml`

- [ ] **Step 1: Create HelmChartProxy**

```yaml
apiVersion: addons.cluster.x-k8s.io/v1alpha1
kind: HelmChartProxy
metadata:
  name: grafana
  namespace: default
spec:
  clusterSelector:
    matchLabels:
      observability: "true"
  repoURL: https://grafana.github.io/helm-charts
  chartName: grafana
  releaseName: grafana
  releaseNamespace: monitoring
  valuesTemplate: |
    adminPassword: "admin"  # Change in production
    datasources:
      datasources.yaml:
        apiVersion: 1
        datasources:
          - name: VictoriaMetrics
            type: prometheus
            url: http://victoriametrics-victoria-metrics-single-server.monitoring.svc:8428
            access: proxy
            isDefault: true
    persistence:
      enabled: true
      size: 5Gi
      storageClass: gp3
```

- [ ] **Step 2: Commit**

```bash
git add platform/caaph/helmchartproxy-grafana.yaml
git commit -m "feat(obs): add Grafana HelmChartProxy for obs cluster"
```

---

### Task 6: Create Kiali HelmChartProxy (obs cluster) + remote secrets

**Files:**
- Create: `platform/caaph/helmchartproxy-kiali.yaml`
- Create: `scripts/phase3b/03-configure-kiali-remotes.sh`

- [ ] **Step 1: Create Kiali HelmChartProxy**

Kiali needs the operator + CR. The HelmChartProxy installs the Kiali Operator, then we apply a Kiali CR separately.

```yaml
apiVersion: addons.cluster.x-k8s.io/v1alpha1
kind: HelmChartProxy
metadata:
  name: kiali-operator
  namespace: default
spec:
  clusterSelector:
    matchLabels:
      observability: "true"
  repoURL: https://kiali.org/helm-charts
  chartName: kiali-operator
  releaseName: kiali-operator
  releaseNamespace: kiali-operator
  valuesTemplate: |
    cr:
      create: true
      namespace: kiali-operator
      spec:
        auth:
          strategy: anonymous
        external_services:
          prometheus:
            url: http://victoriametrics-victoria-metrics-single-server.monitoring.svc:8428
          grafana:
            enabled: true
            in_cluster_url: http://grafana.monitoring.svc:3000
```

- [ ] **Step 2: Create Kiali remote secrets script**

`scripts/phase3b/03-configure-kiali-remotes.sh` (chmod +x):
For each cell cluster, creates a ServiceAccount + ClusterRoleBinding on the cell, extracts the SA token, and creates a Kiali remote secret on the obs cluster.

This follows the Kiali multi-cluster pattern:
1. On each cell: create SA `kiali-remote` with read permissions
2. On obs cluster: create secret labeled `kiali.io/multiCluster: "true"` containing the cell's kubeconfig

- [ ] **Step 3: Commit**

```bash
git add platform/caaph/helmchartproxy-kiali.yaml scripts/phase3b/03-configure-kiali-remotes.sh
git commit -m "feat(obs): add Kiali HelmChartProxy and remote secrets script"
```

---

### Task 7: Create apply script, verification, and README

**Files:**
- Create: `scripts/phase3b/02-apply-helmchartproxies.sh`
- Create: `scripts/phase3b/04-verify.sh`
- Create: `scripts/phase3b/README.md`

- [ ] **Step 1: Create apply script**

Applies all HelmChartProxy CRs to the management cluster and waits for HelmReleaseProxy resources to be created.

```bash
kubectl --context agentic-mgmt apply -f platform/caaph/
# Wait for CAAPH to reconcile
```

- [ ] **Step 2: Create verification script**

Checks:
- CAAPH controller running on management cluster
- HelmReleaseProxy resources created for each cluster
- VictoriaMetrics running on obs cluster
- Grafana running on obs cluster
- Kiali running on obs cluster
- vmagent running on all clusters
- Metrics flowing: query VictoriaMetrics for `up{}` grouped by cluster label

- [ ] **Step 3: Create README**

Run order, prerequisites, what gets created.

- [ ] **Step 4: Commit**

```bash
git add scripts/phase3b/
git commit -m "feat(obs): add Phase 3b apply, verify, and README"
```

---

### Task 8: End-to-end execution

- [ ] **Step 1:** `./scripts/phase3b/00-install-caaph.sh` (~2 min)
- [ ] **Step 2:** `./scripts/phase3b/01-label-clusters.sh` (~30 sec)
- [ ] **Step 3:** `./scripts/phase3b/02-apply-helmchartproxies.sh` (~5-10 min for CAAPH to reconcile)
- [ ] **Step 4:** `./scripts/phase3b/03-configure-kiali-remotes.sh` (~1 min)
- [ ] **Step 5:** `./scripts/phase3b/04-verify.sh`
- [ ] **Step 6:** Commit: `git commit -m "feat(obs): Phase 3b observability stack deployed via CAAPH"`
