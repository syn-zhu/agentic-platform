# Phase 3b: Observability — CAAPH, VictoriaMetrics Operator, VMAuth, Grafana, Kiali, vmagent

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Install CAAPH on the management cluster, deploy the VictoriaMetrics Operator + VMAuth + Grafana + Kiali to the observability cluster, and deploy vmagent to all clusters — establishing a single-pane-of-glass metrics pipeline with proper cluster labels and TLS-secured cross-cluster ingestion.

**Architecture:** CAAPH auto-installs Helm charts onto clusters matched by labels. The obs cluster gets the VictoriaMetrics Operator (which manages VMSingle, VMAuth, VMAgent CRDs), Grafana, and Kiali. VMAuth sits behind an internal NLB as the TLS-terminating auth proxy for cross-cluster metric ingestion. Each cluster runs vmagent that remote-writes to the NLB endpoint with a per-cluster bearer token. Kiali gets remote secrets for cell cluster API access.

**Tech Stack:** CAAPH, VictoriaMetrics Operator (VMSingle, VMAuth, VMAgent, VMUser CRDs), Grafana, Kiali, AWS NLB, cert-manager (obs cluster, for VMAuth TLS)

**Spec:** `docs/superpowers/specs/2026-03-25-multi-cluster-architecture-design.md`

**Pre-requisites:**
- Phase 2 complete (5 clusters running, Gateway API CRDs on all)
- Phase 3a substantially complete (control-plane services running)
- Management cluster has CAPI Operator + CAPA
- AWS LB Controller on obs cluster (needs installing — same pattern as cp)

---

## File Structure

```
platform/
  management-manifests/
    caaph-provider.yaml                    # AddonProvider CR for CAAPH
  caaph/
    helmchartproxy-vm-operator.yaml        # VictoriaMetrics Operator on obs cluster
    helmchartproxy-grafana.yaml            # Grafana on obs cluster
    helmchartproxy-kiali.yaml              # Kiali Operator on obs cluster
    helmchartproxy-vmagent.yaml            # vmagent on ALL clusters
    helmchartproxy-cert-manager.yaml       # cert-manager on obs cluster (for VMAuth TLS)
    helmchartproxy-aws-lb-controller.yaml  # AWS LB controller on obs cluster (for NLB)
  observability-manifests/
    namespaces.yaml                        # obs cluster namespaces
    vmsingle.yaml                          # VMSingle CR (VictoriaMetrics instance)
    vmauth.yaml                            # VMAuth CR (TLS proxy + bearer token auth)
    vmauth-service-nlb.yaml               # Internal NLB Service for VMAuth
    vmuser-per-cluster.yaml               # VMUser CRs (one per cluster, bearer tokens)
    kiali-remote-sa.yaml                   # Kiali remote SA template for cell clusters
scripts/phase3b/
  00-install-caaph.sh                      # Install CAAPH on management cluster
  01-label-clusters.sh                     # Add labels for CAAPH matching
  02-apply-helmchartproxies.sh             # Apply HelmChartProxy CRs (CAAPH installs charts)
  03-apply-obs-manifests.sh                # Apply VM Operator CRs + NLB on obs cluster
  04-configure-kiali-remotes.sh            # Create Kiali remote secrets
  05-verify.sh                             # Smoke tests
  README.md
```

---

### Task 1: Install CAAPH on the management cluster

**Files:**
- Create: `platform/management-manifests/caaph-provider.yaml`
- Create: `scripts/phase3b/00-install-caaph.sh`

- [ ] **Step 1: Create CAAPH AddonProvider manifest**

```yaml
apiVersion: operator.cluster.x-k8s.io/v1alpha2
kind: AddonProvider
metadata:
  name: helm
  namespace: caaph-system
spec:
  version: v0.3.2
```

- [ ] **Step 2: Create install script**

Creates `caaph-system` namespace, applies the AddonProvider CR, waits for controller, verifies HelmChartProxy CRD exists.

- [ ] **Step 3: Commit**

---

### Task 2: Label clusters + create obs namespaces

**Files:**
- Create: `scripts/phase3b/01-label-clusters.sh`
- Create: `platform/observability-manifests/namespaces.yaml`

- [ ] **Step 1: Label clusters for CAAPH matching**

```bash
# All clusters get vmagent
kubectl label cluster agentic-cp agentic-obs agentic-cell-1 agentic-cell-2 \
  -n default monitoring=vmagent --overwrite

# Obs cluster gets the full stack
kubectl label cluster agentic-obs -n default observability=true --overwrite

# Cell clusters get kiali-remote
kubectl label cluster agentic-cell-1 agentic-cell-2 -n default kiali-remote=true --overwrite
```

- [ ] **Step 2: Create obs namespaces**

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: monitoring
---
apiVersion: v1
kind: Namespace
metadata:
  name: kiali-operator
---
apiVersion: v1
kind: Namespace
metadata:
  name: cert-manager
```

- [ ] **Step 3: Commit**

---

### Task 3: Create HelmChartProxies for obs cluster infrastructure

**Files:**
- Create: `platform/caaph/helmchartproxy-cert-manager.yaml` (cert-manager on obs for VMAuth TLS)
- Create: `platform/caaph/helmchartproxy-aws-lb-controller.yaml` (AWS LB controller on obs for NLB)
- Create: `platform/caaph/helmchartproxy-vm-operator.yaml` (VictoriaMetrics Operator)

These are infrastructure prerequisites that must be installed before the VM CRs can be applied.

- [ ] **Step 1: cert-manager HelmChartProxy**

```yaml
apiVersion: addons.cluster.x-k8s.io/v1alpha1
kind: HelmChartProxy
metadata:
  name: cert-manager
  namespace: default
spec:
  clusterSelector:
    matchLabels:
      observability: "true"
  repoURL: https://charts.jetstack.io
  chartName: cert-manager
  version: v1.17.1
  releaseName: cert-manager
  releaseNamespace: cert-manager
  valuesTemplate: |
    crds:
      enabled: true
```

- [ ] **Step 2: AWS LB Controller HelmChartProxy**

Needs IRSA role for the obs cluster (same pattern as control-plane). The role ARN is injected via valuesTemplate from cluster annotations or hardcoded for now.

- [ ] **Step 3: VictoriaMetrics Operator HelmChartProxy**

```yaml
apiVersion: addons.cluster.x-k8s.io/v1alpha1
kind: HelmChartProxy
metadata:
  name: vm-operator
  namespace: default
spec:
  clusterSelector:
    matchLabels:
      observability: "true"
  repoURL: https://victoriametrics.github.io/helm-charts
  chartName: victoria-metrics-operator
  releaseName: vm-operator
  releaseNamespace: monitoring
  valuesTemplate: |
    operator:
      disable_prometheus_converter: false
```

- [ ] **Step 4: Commit**

---

### Task 4: Create HelmChartProxies for Grafana + Kiali

**Files:**
- Create: `platform/caaph/helmchartproxy-grafana.yaml`
- Create: `platform/caaph/helmchartproxy-kiali.yaml`

- [ ] **Step 1: Grafana HelmChartProxy**

Datasource points to VictoriaMetrics at `http://vmsingle-vm.monitoring.svc:8429`.

- [ ] **Step 2: Kiali Operator HelmChartProxy**

Kiali CR configured for anonymous auth, VictoriaMetrics as prometheus endpoint.

- [ ] **Step 3: Commit**

---

### Task 5: Create HelmChartProxy for vmagent (all clusters)

**Files:**
- Create: `platform/caaph/helmchartproxy-vmagent.yaml`

- [ ] **Step 1: Create vmagent HelmChartProxy**

Uses `valuesTemplate` with `{{ .Cluster.metadata.name }}` for per-cluster `externalLabels`. Remote-write URL points to the VMAuth NLB DNS name (set after NLB is provisioned — initially a placeholder, updated in the apply script).

Bearer token per cluster is stored as a Secret in each cluster's `monitoring` namespace, referenced by vmagent config.

- [ ] **Step 2: Commit**

---

### Task 6: Create VictoriaMetrics Operator CRs for obs cluster

**Files:**
- Create: `platform/observability-manifests/vmsingle.yaml` (VMSingle — the VictoriaMetrics instance)
- Create: `platform/observability-manifests/vmauth.yaml` (VMAuth — TLS proxy with per-cluster bearer tokens)
- Create: `platform/observability-manifests/vmauth-service-nlb.yaml` (Internal NLB Service for VMAuth)
- Create: `platform/observability-manifests/vmuser-per-cluster.yaml` (VMUser CRs — one per cluster)

- [ ] **Step 1: VMSingle CR**

```yaml
apiVersion: operator.victoriametrics.com/v1beta1
kind: VMSingle
metadata:
  name: vm
  namespace: monitoring
spec:
  retentionPeriod: "15d"
  storage:
    volumeClaimTemplate:
      spec:
        resources:
          requests:
            storage: 50Gi
        storageClassName: gp3
```

- [ ] **Step 2: VMAuth CR**

VMAuth terminates TLS (cert from cert-manager) and validates per-cluster bearer tokens.

```yaml
apiVersion: operator.victoriametrics.com/v1beta1
kind: VMAuth
metadata:
  name: vmauth
  namespace: monitoring
spec:
  selectAllByDefault: true
  extraArgs:
    tls: "true"
    tlsCertFile: /etc/vmauth-tls/tls.crt
    tlsKeyFile: /etc/vmauth-tls/tls.key
  extraVolumes:
    - name: tls
      secret:
        secretName: vmauth-tls-cert
  extraVolumeMounts:
    - name: tls
      mountPath: /etc/vmauth-tls
      readOnly: true
```

- [ ] **Step 3: VMAuth NLB Service**

```yaml
apiVersion: v1
kind: Service
metadata:
  name: vmauth-external
  namespace: monitoring
  annotations:
    service.beta.kubernetes.io/aws-load-balancer-type: "external"
    service.beta.kubernetes.io/aws-load-balancer-scheme: "internal"
    service.beta.kubernetes.io/aws-load-balancer-nlb-target-type: "ip"
spec:
  type: LoadBalancer
  selector:
    app.kubernetes.io/name: vmauth
    app.kubernetes.io/instance: vmauth
  ports:
    - name: https
      port: 443
      targetPort: 8427
```

- [ ] **Step 4: VMUser CRs (one per cluster)**

```yaml
apiVersion: operator.victoriametrics.com/v1beta1
kind: VMUser
metadata:
  name: cluster-agentic-cp
  namespace: monitoring
spec:
  generatePassword: true
  targetRefs:
    - static:
        url: http://vmsingle-vm.monitoring.svc:8429
      paths: ["/api/v1/write", "/insert/.*"]
---
# Repeat for agentic-obs, agentic-cell-1, agentic-cell-2
```

- [ ] **Step 5: cert-manager Certificate for VMAuth TLS**

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: vmauth-tls-cert
  namespace: monitoring
spec:
  secretName: vmauth-tls-cert
  issuerRef:
    name: selfsigned
    kind: ClusterIssuer
  dnsNames:
    - vmauth.monitoring.svc
    - vmauth-external.monitoring.svc
    - "*.elb.us-east-1.amazonaws.com"
```

- [ ] **Step 6: Commit**

---

### Task 7: Create deployment scripts + verification

**Files:**
- Create: `scripts/phase3b/02-apply-helmchartproxies.sh`
- Create: `scripts/phase3b/03-apply-obs-manifests.sh`
- Create: `scripts/phase3b/04-configure-kiali-remotes.sh`
- Create: `scripts/phase3b/05-verify.sh`
- Create: `scripts/phase3b/README.md`

- [ ] **Step 1: Apply HelmChartProxies script**

Applies all HelmChartProxy CRs to management cluster. CAAPH auto-installs charts. Script waits for HelmReleaseProxy resources to appear.

- [ ] **Step 2: Apply obs manifests script**

After CAAPH installs the VM Operator, this script applies the VMSingle, VMAuth, VMUser, NLB Service, and cert-manager Certificate CRs to the obs cluster.

Also extracts the VMUser generated passwords and distributes them as Secrets to each cluster's `monitoring` namespace (for vmagent to use as bearer tokens).

Also extracts the VMAuth NLB DNS name and updates vmagent config with the actual endpoint.

- [ ] **Step 3: Configure Kiali remotes script**

Creates Kiali remote SA + ClusterRoleBinding on each cell cluster, extracts tokens, creates Kiali remote secrets on obs cluster.

- [ ] **Step 4: Verification script**

Checks: CAAPH running, VictoriaMetrics running, Grafana running, Kiali running, vmagent running on all clusters, metrics flowing (query VM for `up{}` grouped by cluster).

- [ ] **Step 5: README**

- [ ] **Step 6: Commit**

---

### Task 8: End-to-end execution

- [ ] **Step 1:** `./scripts/phase3b/00-install-caaph.sh` (~2 min)
- [ ] **Step 2:** `./scripts/phase3b/01-label-clusters.sh` (~30 sec)
- [ ] **Step 3:** `./scripts/phase3b/02-apply-helmchartproxies.sh` (~10 min for CAAPH to reconcile all charts)
- [ ] **Step 4:** `./scripts/phase3b/03-apply-obs-manifests.sh` (~5 min — VM CRs, NLB, token distribution)
- [ ] **Step 5:** `./scripts/phase3b/04-configure-kiali-remotes.sh` (~1 min)
- [ ] **Step 6:** `./scripts/phase3b/05-verify.sh`
- [ ] **Step 7:** Commit
