# Phase 2: Cluster Provisioning + Per-Cluster Istio

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Provision 4 EKS clusters (control-plane, observability, cell-1, cell-2) with CAPA, connect them via Transit Gateway, and install Istio independently on each — sidecar mode on control-plane, ambient mode on cells, no mesh on observability.

**Architecture:** The control-plane cluster is created first via eksctl (bootstrap), then CAPA is installed on it to manage the remaining 3 clusters declaratively. Each cell gets an independent Istio ambient mesh. The control-plane gets Istio in sidecar mode. The observability cluster has no mesh. Cross-cluster communication uses standard HTTPS over TGW.

**Tech Stack:** eksctl, CAPA (EKS), Istio 1.29 (Helm), AWS Transit Gateway

**Spec:** `docs/superpowers/specs/2026-03-25-multi-cluster-architecture-design.md`

**Pre-requisites:**
- AWS CLI configured (`AWS_PROFILE` set)
- `eksctl`, `kubectl`, `helm`, `helmfile`, `envsubst` installed
- Previous Phase 1 resources torn down (management cluster deleted)

---

## File Structure

```
cluster/
  control-plane/
    cluster.yaml                        # eksctl config (bootstraps CAPA host)
    iam-policies/
      capa-controller-policy.json       # IAM policy for CAPA
  observability/
    cluster.yaml                        # CAPA: Cluster + AWSManagedControlPlane + AWSManagedCluster
    machinepool.yaml                    # CAPA: MachinePool + AWSManagedMachinePool
  cell-1/
    cluster.yaml                        # CAPA CRs
    machinepool.yaml
  cell-2/
    cluster.yaml                        # CAPA CRs
    machinepool.yaml
  shared/
    storageclass-gp3.yaml              # gp3 StorageClass (applied to all clusters)
  transit-gateway/
    create-tgw.sh                       # (already exists from Phase 1, updated)
    teardown-tgw.sh
platform/
  control-plane/
    helmfile.yaml                       # CAPA + cert-manager + Istio (sidecar)
    values/
      capa.yaml
      cert-manager.yaml
      istiod-sidecar.yaml
  cell/
    helmfile.yaml                       # Istio ambient
    values/
      istiod-ambient.yaml
      istio-cni.yaml
      ztunnel.yaml
  management-manifests/
    capi-providers.yaml                 # (already exists, reused)
scripts/phase2/
    00-create-control-plane.sh          # eksctl create, install CAPA
    01-create-transit-gateway.sh        # TGW + attach control-plane VPC
    02-provision-clusters.sh            # CAPA provisions obs, cell-1, cell-2
    03-attach-vpcs-to-tgw.sh            # Attach remaining VPCs, add routes + SG rules
    04-install-istio.sh                 # Istio on control-plane (sidecar) + cells (ambient)
    05-verify.sh                        # Smoke tests
    README.md
```

---

### Task 1: Create control-plane cluster config (eksctl bootstrap)

**Files:**
- Create: `cluster/control-plane/cluster.yaml`
- Move: `cluster/management/iam-policies/capa-controller-policy.json` → `cluster/control-plane/iam-policies/capa-controller-policy.json`
- Create: `cluster/shared/storageclass-gp3.yaml`

- [ ] **Step 1: Create eksctl config**

`cluster/control-plane/cluster.yaml` — the control-plane cluster is created via eksctl (not CAPA) since it hosts CAPA itself:

```yaml
apiVersion: eksctl.io/v1alpha5
kind: ClusterConfig

metadata:
  name: agentic-cp
  region: us-east-1
  version: "1.31"

vpc:
  cidr: "10.1.0.0/16"
  nat:
    gateway: Single

iam:
  withOIDC: true

addons:
  - name: vpc-cni
    version: latest
    configurationValues: '{"enableNetworkPolicy":"true"}'
  - name: coredns
    version: latest
  - name: kube-proxy
    version: latest
  - name: aws-ebs-csi-driver
    version: latest

managedNodeGroups:
  - name: platform
    instanceType: t3.large
    minSize: 2
    maxSize: 4
    desiredCapacity: 2
    labels:
      node-role: platform
    volumeSize: 30
    volumeType: gp3
    tags:
      role: platform
      cluster: agentic-cp
```

- [ ] **Step 2: Move CAPA IAM policy to control-plane directory**

```bash
mkdir -p cluster/control-plane/iam-policies
mv cluster/management/iam-policies/capa-controller-policy.json cluster/control-plane/iam-policies/
rm -rf cluster/management/
```

- [ ] **Step 3: Create shared gp3 StorageClass manifest**

`cluster/shared/storageclass-gp3.yaml`:
```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: gp3
  annotations:
    storageclass.kubernetes.io/is-default-class: "true"
provisioner: ebs.csi.aws.com
parameters:
  type: gp3
  encrypted: "true"
reclaimPolicy: Delete
volumeBindingMode: WaitForFirstConsumer
allowVolumeExpansion: true
```

- [ ] **Step 4: Commit**

```bash
git add cluster/control-plane/ cluster/shared/
git rm -r cluster/management/ 2>/dev/null || true
git commit -m "feat(infra): add control-plane eksctl config, move CAPA policy, add shared storageclass"
```

---

### Task 2: Create CAPA-managed cluster manifests (obs, cell-1, cell-2)

**Files:**
- Create: `cluster/observability/cluster.yaml`, `cluster/observability/machinepool.yaml`
- Create: `cluster/cell-1/cluster.yaml`, `cluster/cell-1/machinepool.yaml`
- Create: `cluster/cell-2/cluster.yaml`, `cluster/cell-2/machinepool.yaml`

- [ ] **Step 1: Create observability cluster manifests**

`cluster/observability/cluster.yaml`:
```yaml
apiVersion: cluster.x-k8s.io/v1beta1
kind: Cluster
metadata:
  name: agentic-obs
  namespace: default
spec:
  clusterNetwork:
    pods:
      cidrBlocks: ["192.168.0.0/16"]
    services:
      cidrBlocks: ["10.96.0.0/12"]
  infrastructureRef:
    apiVersion: infrastructure.cluster.x-k8s.io/v1beta2
    kind: AWSManagedCluster
    name: agentic-obs
  controlPlaneRef:
    apiVersion: controlplane.cluster.x-k8s.io/v1beta2
    kind: AWSManagedControlPlane
    name: agentic-obs
---
apiVersion: infrastructure.cluster.x-k8s.io/v1beta2
kind: AWSManagedCluster
metadata:
  name: agentic-obs
  namespace: default
spec: {}
---
apiVersion: controlplane.cluster.x-k8s.io/v1beta2
kind: AWSManagedControlPlane
metadata:
  name: agentic-obs
  namespace: default
spec:
  eksClusterName: agentic-obs
  region: us-east-1
  version: v1.31.0
  network:
    vpc:
      cidrBlock: "10.2.0.0/16"
  associateOIDCProvider: true
  addons:
    - name: vpc-cni
      version: latest
      conflictResolution: overwrite
    - name: coredns
      version: latest
      conflictResolution: overwrite
    - name: kube-proxy
      version: latest
      conflictResolution: overwrite
    - name: aws-ebs-csi-driver
      version: latest
      conflictResolution: overwrite
  endpointAccess:
    public: true
    private: true
  bastion:
    enabled: false
  additionalTags:
    project: agentic-platform
    cluster-type: observability
```

`cluster/observability/machinepool.yaml`:
```yaml
apiVersion: cluster.x-k8s.io/v1beta1
kind: MachinePool
metadata:
  name: agentic-obs-pool
  namespace: default
spec:
  clusterName: agentic-obs
  replicas: 2
  template:
    spec:
      clusterName: agentic-obs
      bootstrap:
        dataSecretName: ""
      infrastructureRef:
        apiVersion: infrastructure.cluster.x-k8s.io/v1beta2
        kind: AWSManagedMachinePool
        name: agentic-obs-pool
---
apiVersion: infrastructure.cluster.x-k8s.io/v1beta2
kind: AWSManagedMachinePool
metadata:
  name: agentic-obs-pool
  namespace: default
spec:
  instanceType: t3.large
  scaling:
    minSize: 2
    maxSize: 3
  diskSize: 30
  labels:
    node-role: obs
  capacityType: onDemand
  additionalTags:
    project: agentic-platform
    role: obs
```

- [ ] **Step 2: Create cell-1 cluster manifests**

Same structure as observability, but:
- Names: `agentic-cell-1`
- VPC CIDR: `10.3.0.0/16`
- Tag: `cluster-type: cell`
- Three machine pools:

`cluster/cell-1/machinepool.yaml`:
```yaml
# Workload pool — tenant agent pods, MCP servers, sandboxes
apiVersion: cluster.x-k8s.io/v1beta1
kind: MachinePool
metadata:
  name: agentic-cell-1-workload
  namespace: default
spec:
  clusterName: agentic-cell-1
  replicas: 1
  template:
    spec:
      clusterName: agentic-cell-1
      bootstrap:
        dataSecretName: ""
      infrastructureRef:
        apiVersion: infrastructure.cluster.x-k8s.io/v1beta2
        kind: AWSManagedMachinePool
        name: agentic-cell-1-workload
---
apiVersion: infrastructure.cluster.x-k8s.io/v1beta2
kind: AWSManagedMachinePool
metadata:
  name: agentic-cell-1-workload
  namespace: default
spec:
  instanceType: t3.large
  scaling:
    minSize: 1
    maxSize: 10
  diskSize: 30
  labels:
    node-role: workload
  taints:
    - key: role
      value: workload
      effect: no-schedule
  capacityType: onDemand
  additionalTags:
    project: agentic-platform
    role: workload
---
# Waypoint pool — per-tenant agentgateway waypoints
apiVersion: cluster.x-k8s.io/v1beta1
kind: MachinePool
metadata:
  name: agentic-cell-1-waypoint
  namespace: default
spec:
  clusterName: agentic-cell-1
  replicas: 1
  template:
    spec:
      clusterName: agentic-cell-1
      bootstrap:
        dataSecretName: ""
      infrastructureRef:
        apiVersion: infrastructure.cluster.x-k8s.io/v1beta2
        kind: AWSManagedMachinePool
        name: agentic-cell-1-waypoint
---
apiVersion: infrastructure.cluster.x-k8s.io/v1beta2
kind: AWSManagedMachinePool
metadata:
  name: agentic-cell-1-waypoint
  namespace: default
spec:
  instanceType: t3.small
  scaling:
    minSize: 1
    maxSize: 5
  diskSize: 20
  labels:
    node-role: waypoint
  taints:
    - key: role
      value: waypoint
      effect: no-schedule
  capacityType: onDemand
  additionalTags:
    project: agentic-platform
    role: waypoint
---
# Gateway pool — cell gateway (NLB-backed)
apiVersion: cluster.x-k8s.io/v1beta1
kind: MachinePool
metadata:
  name: agentic-cell-1-gateway
  namespace: default
spec:
  clusterName: agentic-cell-1
  replicas: 1
  template:
    spec:
      clusterName: agentic-cell-1
      bootstrap:
        dataSecretName: ""
      infrastructureRef:
        apiVersion: infrastructure.cluster.x-k8s.io/v1beta2
        kind: AWSManagedMachinePool
        name: agentic-cell-1-gateway
---
apiVersion: infrastructure.cluster.x-k8s.io/v1beta2
kind: AWSManagedMachinePool
metadata:
  name: agentic-cell-1-gateway
  namespace: default
spec:
  instanceType: t3.medium
  scaling:
    minSize: 1
    maxSize: 2
  diskSize: 20
  labels:
    node-role: gateway
  taints:
    - key: role
      value: gateway
      effect: no-schedule
  capacityType: onDemand
  additionalTags:
    project: agentic-platform
    role: gateway
```

- [ ] **Step 3: Create cell-2 cluster manifests**

Identical to cell-1 but with names `agentic-cell-2` and VPC CIDR `10.4.0.0/16`.

- [ ] **Step 4: Commit**

```bash
git add cluster/observability/ cluster/cell-1/ cluster/cell-2/
git commit -m "feat(infra): add CAPA cluster manifests for observability, cell-1, cell-2"
```

---

### Task 3: Create control-plane helmfile (CAPA + Istio sidecar)

**Files:**
- Create: `platform/control-plane/helmfile.yaml`
- Create: `platform/control-plane/values/cert-manager.yaml`
- Create: `platform/control-plane/values/capa.yaml`
- Create: `platform/control-plane/values/istiod-sidecar.yaml`
- Move: `platform/management-manifests/capi-providers.yaml` → `platform/control-plane-manifests/capi-providers.yaml`

- [ ] **Step 1: Create control-plane helmfile**

`platform/control-plane/helmfile.yaml`:
```yaml
repositories:
  - name: jetstack
    url: https://charts.jetstack.io
  - name: capi
    url: https://kubernetes-sigs.github.io/cluster-api-operator
  - name: istio
    url: https://istio-release.storage.googleapis.com/charts

helmDefaults:
  createNamespace: true
  wait: true
  timeout: 300

releases:
  - name: cert-manager
    namespace: cert-manager
    chart: jetstack/cert-manager
    version: "v1.17.1"
    values:
      - values/cert-manager.yaml

  - name: capi-operator
    namespace: capi-system
    chart: capi/cluster-api-operator
    version: "0.26.0"
    values:
      - values/capa.yaml
    needs:
      - cert-manager/cert-manager

  - name: istio-base
    namespace: istio-system
    chart: istio/base
    version: "1.29.1"

  - name: istiod
    namespace: istio-system
    chart: istio/istiod
    version: "1.29.1"
    values:
      - values/istiod-sidecar.yaml
    needs:
      - istio-system/istio-base
```

- [ ] **Step 2: Create values files**

`platform/control-plane/values/cert-manager.yaml`:
```yaml
crds:
  enabled: true
replicaCount: 2
resources:
  requests:
    cpu: 50m
    memory: 128Mi
  limits:
    memory: 256Mi
```

`platform/control-plane/values/capa.yaml`:
```yaml
resources:
  manager:
    requests:
      cpu: 50m
      memory: 128Mi
    limits:
      memory: 256Mi
```

`platform/control-plane/values/istiod-sidecar.yaml`:
```yaml
# Istio sidecar mode — internal control-plane service mesh only
profile: default
```

- [ ] **Step 3: Move CAPI providers manifest**

```bash
mkdir -p platform/control-plane-manifests
mv platform/management-manifests/capi-providers.yaml platform/control-plane-manifests/
rm -rf platform/management-manifests/ platform/management/
```

- [ ] **Step 4: Commit**

```bash
git add platform/control-plane/ platform/control-plane-manifests/
git rm -r platform/management/ platform/management-manifests/ 2>/dev/null || true
git commit -m "feat(infra): add control-plane helmfile (CAPA + cert-manager + Istio sidecar)"
```

---

### Task 4: Create cell helmfile (Istio ambient)

**Files:**
- Create: `platform/cell/helmfile.yaml`
- Create: `platform/cell/values/istiod-ambient.yaml`
- Create: `platform/cell/values/istio-cni.yaml`
- Create: `platform/cell/values/ztunnel.yaml`

- [ ] **Step 1: Create cell helmfile**

`platform/cell/helmfile.yaml`:
```yaml
repositories:
  - name: istio
    url: https://istio-release.storage.googleapis.com/charts

helmDefaults:
  createNamespace: true
  wait: true
  timeout: 300

releases:
  - name: istio-base
    namespace: istio-system
    chart: istio/base
    version: "1.29.1"

  - name: istiod
    namespace: istio-system
    chart: istio/istiod
    version: "1.29.1"
    values:
      - values/istiod-ambient.yaml
    needs:
      - istio-system/istio-base

  - name: istio-cni
    namespace: istio-system
    chart: istio/cni
    version: "1.29.1"
    values:
      - values/istio-cni.yaml
    needs:
      - istio-system/istiod

  - name: ztunnel
    namespace: istio-system
    chart: istio/ztunnel
    version: "1.29.1"
    values:
      - values/ztunnel.yaml
    needs:
      - istio-system/istio-cni
```

- [ ] **Step 2: Create values files**

`platform/cell/values/istiod-ambient.yaml`:
```yaml
# Istio ambient mode — cell-local mesh, no multi-cluster
profile: ambient
```

`platform/cell/values/istio-cni.yaml`:
```yaml
profile: ambient
```

`platform/cell/values/ztunnel.yaml`:
```yaml
# Single-cluster ambient — no multi-cluster config needed
```

- [ ] **Step 3: Commit**

```bash
git add platform/cell/
git commit -m "feat(istio): add cell helmfile for independent ambient mesh"
```

---

### Task 5: Create deployment scripts

**Files:**
- Create: `scripts/phase2/00-create-control-plane.sh`
- Create: `scripts/phase2/01-create-transit-gateway.sh`
- Create: `scripts/phase2/02-provision-clusters.sh`
- Create: `scripts/phase2/03-attach-vpcs-to-tgw.sh`
- Create: `scripts/phase2/04-install-istio.sh`
- Create: `scripts/phase2/05-verify.sh`
- Create: `scripts/phase2/README.md`

- [ ] **Step 1: Write 00-create-control-plane.sh**

Creates the control-plane EKS cluster via eksctl, installs CAPA + cert-manager + Istio sidecar via helmfile, and applies CAPI provider CRs.

```bash
#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

echo "=== Phase 2.0: Creating Control-Plane Cluster ==="

REGION="us-east-1"
CLUSTER_NAME="agentic-cp"
ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
echo "AWS Account ID: $ACCOUNT_ID"

# ── 1. Create CAPA IAM policy ──
echo "Creating CAPA controller IAM policy..."
aws iam create-policy \
  --policy-name capa-controller \
  --policy-document "file://$ROOT_DIR/cluster/control-plane/iam-policies/capa-controller-policy.json" \
  2>/dev/null || echo "  Policy already exists, skipping."

# ── 2. Create EKS cluster ──
echo "Creating control-plane EKS cluster (this will take ~15-20 minutes)..."
eksctl create cluster -f "$ROOT_DIR/cluster/control-plane/cluster.yaml"

# ── 3. Update kubeconfig ──
echo "Updating kubeconfig..."
aws eks update-kubeconfig --name "$CLUSTER_NAME" --region "$REGION" --alias "$CLUSTER_NAME"

# ── 4. Apply gp3 StorageClass ──
echo "Creating gp3 StorageClass..."
kubectl --context "$CLUSTER_NAME" apply -f "$ROOT_DIR/cluster/shared/storageclass-gp3.yaml"

# ── 5. Install Gateway API CRDs (needed for Istio) ──
echo "Installing Gateway API CRDs..."
kubectl --context "$CLUSTER_NAME" get crd gateways.gateway.networking.k8s.io > /dev/null 2>&1 || \
  kubectl --context "$CLUSTER_NAME" apply --server-side \
    -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.4.0/experimental-install.yaml

# ── 6. Deploy CAPA + cert-manager + Istio sidecar via helmfile ──
echo "Installing control-plane components via helmfile..."
cd "$ROOT_DIR/platform/control-plane"
helmfile --kube-context "$CLUSTER_NAME" sync
cd "$ROOT_DIR"

# ── 7. Set up CAPA IRSA ──
echo "Setting up CAPA IRSA..."
OIDC_PROVIDER=$(aws eks describe-cluster --name "$CLUSTER_NAME" --region "$REGION" \
  --query 'cluster.identity.oidc.issuer' --output text | sed 's|https://||')

CAPA_ROLE_NAME="agentic-cp-capa-controller"
CAPA_POLICY_ARN="arn:aws:iam::${ACCOUNT_ID}:policy/capa-controller"

TRUST_POLICY=$(mktemp)
cat > "$TRUST_POLICY" <<TRUST
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": {
      "Federated": "arn:aws:iam::${ACCOUNT_ID}:oidc-provider/${OIDC_PROVIDER}"
    },
    "Action": "sts:AssumeRoleWithWebIdentity",
    "Condition": {
      "StringEquals": {
        "${OIDC_PROVIDER}:sub": "system:serviceaccount:capa-system:capa-controller-manager",
        "${OIDC_PROVIDER}:aud": "sts.amazonaws.com"
      }
    }
  }]
}
TRUST

aws iam create-role --role-name "$CAPA_ROLE_NAME" \
  --assume-role-policy-document "file://$TRUST_POLICY" \
  --tags "Key=project,Value=agentic-platform" \
  2>/dev/null || echo "  CAPA IAM role already exists."
rm -f "$TRUST_POLICY"

aws iam attach-role-policy --role-name "$CAPA_ROLE_NAME" \
  --policy-arn "$CAPA_POLICY_ARN" 2>/dev/null || echo "  Policy already attached."

CAPA_ROLE_ARN="arn:aws:iam::${ACCOUNT_ID}:role/${CAPA_ROLE_NAME}"

# ── 8. Apply CAPI provider CRs ──
echo "Creating CAPI Core + Infrastructure providers..."
kubectl --context "$CLUSTER_NAME" create namespace capa-system \
  --dry-run=client -o yaml | kubectl --context "$CLUSTER_NAME" apply -f -

# Create AWS credentials secret (required by CAPA even with IRSA)
kubectl --context "$CLUSTER_NAME" -n capa-system create secret generic aws-credentials \
  --from-literal=AWS_B64ENCODED_CREDENTIALS="$(printf '[default]\naws_access_key_id = \naws_secret_access_key = \n' | base64)" \
  --dry-run=client -o yaml | kubectl --context "$CLUSTER_NAME" apply -f -

kubectl --context "$CLUSTER_NAME" apply -f "$ROOT_DIR/platform/control-plane-manifests/capi-providers.yaml"

# ── 9. Wait for CAPA + annotate with IRSA ──
echo "Waiting for CAPI core controller..."
sleep 15
kubectl --context "$CLUSTER_NAME" -n capi-system wait deployment --all \
  --for=condition=Available --timeout=180s 2>/dev/null || true

echo "Waiting for CAPA controller..."
sleep 30
kubectl --context "$CLUSTER_NAME" -n capa-system wait deployment --all \
  --for=condition=Available --timeout=180s 2>/dev/null || true

if kubectl --context "$CLUSTER_NAME" -n capa-system get sa capa-controller-manager > /dev/null 2>&1; then
  kubectl --context "$CLUSTER_NAME" -n capa-system annotate sa capa-controller-manager \
    "eks.amazonaws.com/role-arn=$CAPA_ROLE_ARN" --overwrite
  kubectl --context "$CLUSTER_NAME" -n capa-system rollout restart deployment
  kubectl --context "$CLUSTER_NAME" -n capa-system wait deployment --all \
    --for=condition=Available --timeout=120s 2>/dev/null || true
fi

echo ""
echo "=== Control-plane cluster ready ==="
echo "Context: $CLUSTER_NAME"
echo "Components: CAPA, cert-manager, Istio (sidecar mode)"
```

- [ ] **Step 2: Write 01-create-transit-gateway.sh**

Reuses `cluster/transit-gateway/create-tgw.sh` and adds routes for all 4 VPCs:

```bash
#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

echo "=== Phase 2.1: Transit Gateway Setup ==="

REGION="us-east-1"

# Create TGW and attach control-plane VPC
"$ROOT_DIR/cluster/transit-gateway/create-tgw.sh"

TGW_ID=$(cat "$ROOT_DIR/cluster/transit-gateway/.tgw-id")
CP_VPC_ID=$(aws eks describe-cluster --name "agentic-cp" --region "$REGION" \
  --query 'cluster.resourcesVpcConfig.vpcId' --output text)

# Add routes from control-plane VPC to future cluster CIDRs
echo "Adding VPC route table entries..."
ROUTE_TABLES=($(aws ec2 describe-route-tables \
  --filters "Name=vpc-id,Values=$CP_VPC_ID" \
  --query 'RouteTables[].RouteTableId' --output text --region "$REGION"))

REMOTE_CIDRS=("10.2.0.0/16" "10.3.0.0/16" "10.4.0.0/16")
for RT in "${ROUTE_TABLES[@]}"; do
  for CIDR in "${REMOTE_CIDRS[@]}"; do
    aws ec2 create-route --route-table-id "$RT" \
      --destination-cidr-block "$CIDR" --transit-gateway-id "$TGW_ID" \
      --region "$REGION" 2>/dev/null || true
  done
done

echo "=== Transit Gateway setup complete ==="
```

- [ ] **Step 3: Write 02-provision-clusters.sh**

```bash
#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

echo "=== Phase 2.2: Provisioning Clusters via CAPA ==="

CP_CTX="agentic-cp"
REGION="us-east-1"
CLUSTER_DIRS=("observability" "cell-1" "cell-2")
EKS_NAMES=("agentic-obs" "agentic-cell-1" "agentic-cell-2")

# Apply CAPA CRs
for DIR in "${CLUSTER_DIRS[@]}"; do
  echo "── Provisioning: $DIR ──"
  kubectl --context "$CP_CTX" apply -f "$ROOT_DIR/cluster/$DIR/cluster.yaml"
  kubectl --context "$CP_CTX" apply -f "$ROOT_DIR/cluster/$DIR/machinepool.yaml"
done

# Wait for clusters
echo ""
echo "Waiting for clusters (15-20 minutes)..."
for NAME in "${EKS_NAMES[@]}"; do
  echo "  Waiting for $NAME..."
  kubectl --context "$CP_CTX" -n default wait cluster "$NAME" \
    --for=condition=Ready --timeout=1200s 2>/dev/null || \
    echo "  WARNING: $NAME not ready yet."
done

# Update kubeconfigs and apply gp3 StorageClass
for NAME in "${EKS_NAMES[@]}"; do
  aws eks update-kubeconfig --name "$NAME" --region "$REGION" --alias "$NAME" 2>/dev/null || true
  kubectl --context "$NAME" apply -f "$ROOT_DIR/cluster/shared/storageclass-gp3.yaml" 2>/dev/null || true
done

echo ""
echo "=== Clusters provisioned: ${EKS_NAMES[*]} ==="
```

- [ ] **Step 4: Write 03-attach-vpcs-to-tgw.sh**

```bash
#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

echo "=== Phase 2.3: Attaching VPCs to Transit Gateway ==="

REGION="us-east-1"
TGW_ID=$(cat "$ROOT_DIR/cluster/transit-gateway/.tgw-id")
EKS_NAMES=("agentic-obs" "agentic-cell-1" "agentic-cell-2")
ALL_CIDRS=("10.1.0.0/16" "10.2.0.0/16" "10.3.0.0/16" "10.4.0.0/16")

for EKS_NAME in "${EKS_NAMES[@]}"; do
  echo "── Attaching: $EKS_NAME ──"
  VPC_ID=$(aws eks describe-cluster --name "$EKS_NAME" --region "$REGION" \
    --query 'cluster.resourcesVpcConfig.vpcId' --output text 2>/dev/null || echo "")
  [[ -z "$VPC_ID" || "$VPC_ID" == "None" ]] && echo "  Skipping — not found." && continue

  SUBNETS=($(aws ec2 describe-subnets \
    --filters "Name=vpc-id,Values=$VPC_ID" "Name=map-public-ip-on-launch,Values=false" \
    --query 'Subnets[].SubnetId' --output text --region "$REGION"))

  aws ec2 create-transit-gateway-vpc-attachment \
    --transit-gateway-id "$TGW_ID" --vpc-id "$VPC_ID" \
    --subnet-ids "${SUBNETS[@]}" \
    --tag-specifications "ResourceType=transit-gateway-attachment,Tags=[{Key=Name,Value=$EKS_NAME},{Key=project,Value=agentic-platform}]" \
    --region "$REGION" 2>/dev/null || echo "  Attachment exists."

  # Add routes to all other CIDRs
  VPC_CIDR=$(aws ec2 describe-vpcs --vpc-ids "$VPC_ID" \
    --query 'Vpcs[0].CidrBlock' --output text --region "$REGION")
  ROUTE_TABLES=($(aws ec2 describe-route-tables \
    --filters "Name=vpc-id,Values=$VPC_ID" \
    --query 'RouteTables[].RouteTableId' --output text --region "$REGION"))

  for RT in "${ROUTE_TABLES[@]}"; do
    for CIDR in "${ALL_CIDRS[@]}"; do
      [[ "$CIDR" == "$VPC_CIDR" ]] && continue
      aws ec2 create-route --route-table-id "$RT" \
        --destination-cidr-block "$CIDR" --transit-gateway-id "$TGW_ID" \
        --region "$REGION" 2>/dev/null || true
    done
  done

  # Add SG rule for HTTPS from all VPCs
  SG_ID=$(aws eks describe-cluster --name "$EKS_NAME" --region "$REGION" \
    --query 'cluster.resourcesVpcConfig.clusterSecurityGroupId' --output text)
  for CIDR in "${ALL_CIDRS[@]}"; do
    aws ec2 authorize-security-group-ingress \
      --group-id "$SG_ID" --protocol tcp --port 443 --cidr "$CIDR" \
      --region "$REGION" 2>/dev/null || true
  done
  echo "  Done."
done

echo "=== VPC attachments complete ==="
```

- [ ] **Step 5: Write 04-install-istio.sh**

```bash
#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

echo "=== Phase 2.4: Installing Istio ==="

CELL_CLUSTERS=("agentic-cell-1" "agentic-cell-2")

for CLUSTER in "${CELL_CLUSTERS[@]}"; do
  echo ""
  echo "── Installing Istio ambient on $CLUSTER ──"

  # Install Gateway API CRDs
  kubectl --context "$CLUSTER" get crd gateways.gateway.networking.k8s.io > /dev/null 2>&1 || \
    kubectl --context "$CLUSTER" apply --server-side \
      -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.4.0/experimental-install.yaml

  # Install Istio ambient via helmfile
  cd "$ROOT_DIR/platform/cell"
  helmfile --kube-context "$CLUSTER" sync
  cd "$ROOT_DIR"

  # Wait for istiod
  kubectl --context "$CLUSTER" -n istio-system rollout status deployment/istiod --timeout=120s
  echo "  ✓ Istio ambient installed on $CLUSTER"
done

echo ""
echo "=== Istio installed on all cell clusters ==="
echo "(Control-plane Istio sidecar was installed in step 00)"
```

- [ ] **Step 6: Write 05-verify.sh**

```bash
#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

REGION="us-east-1"
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

echo "=== Phase 2 Verification ==="

echo ""
echo "── Control-Plane (agentic-cp) ──"
check "Cluster reachable" "kubectl --context agentic-cp cluster-info"
check "istiod running (sidecar)" "kubectl --context agentic-cp -n istio-system get deployment istiod -o jsonpath='{.status.readyReplicas}' | grep -qE '[0-9]+'"
check "CAPA running" "kubectl --context agentic-cp -n capa-system get deployment -o jsonpath='{.items[0].status.readyReplicas}' | grep -qE '[0-9]+'"
check "cert-manager running" "kubectl --context agentic-cp -n cert-manager get deployment cert-manager -o jsonpath='{.status.readyReplicas}' | grep -qE '[0-9]+'"

echo ""
echo "── Observability (agentic-obs) ──"
check "Cluster reachable" "kubectl --context agentic-obs cluster-info"
check "Nodes ready" "kubectl --context agentic-obs get nodes -o jsonpath='{.items[0].status.conditions[?(@.type==\"Ready\")].status}' | grep -q True"

for CELL in agentic-cell-1 agentic-cell-2; do
  echo ""
  echo "── $CELL ──"
  check "Cluster reachable" "kubectl --context $CELL cluster-info"
  check "istiod running (ambient)" "kubectl --context $CELL -n istio-system get deployment istiod -o jsonpath='{.status.readyReplicas}' | grep -qE '[0-9]+'"
  check "ztunnel running" "kubectl --context $CELL -n istio-system get daemonset ztunnel -o jsonpath='{.status.numberReady}' | grep -qE '[0-9]+'"
  check "istio-cni running" "kubectl --context $CELL -n istio-system get daemonset istio-cni-node -o jsonpath='{.status.numberReady}' | grep -qE '[0-9]+'"
done

echo ""
echo "── Transit Gateway ──"
TGW_ID=$(cat "$ROOT_DIR/cluster/transit-gateway/.tgw-id" 2>/dev/null || echo "")
if [[ -n "$TGW_ID" ]]; then
  ATTACHMENTS=$(aws ec2 describe-transit-gateway-vpc-attachments \
    --filters "Name=transit-gateway-id,Values=$TGW_ID" "Name=state,Values=available" \
    --region "$REGION" --query 'length(TransitGatewayVpcAttachments)' --output text)
  check "TGW has 4 VPC attachments" "test $ATTACHMENTS -ge 4"
else
  echo "  ✗ TGW not found"
  FAIL=$((FAIL + 1))
fi

echo ""
echo "════════════════════════════"
echo "Results: $PASS passed, $FAIL failed"
if [[ $FAIL -gt 0 ]]; then
  echo "PHASE 2 NOT READY — fix failures above"
  exit 1
else
  echo "PHASE 2 READY — proceed to Phase 3 (deploy platform services + observability stack)"
fi
```

- [ ] **Step 7: Write README.md**

```markdown
# Phase 2: Cluster Provisioning + Per-Cluster Istio

## Prerequisites

- AWS CLI configured (`AWS_PROFILE` set)
- `eksctl`, `kubectl`, `helm`, `helmfile`, `envsubst` installed

## Run Order

\`\`\`bash
./scripts/phase2/00-create-control-plane.sh     # ~20 min (EKS + CAPA + Istio)
./scripts/phase2/01-create-transit-gateway.sh    # ~3 min
./scripts/phase2/02-provision-clusters.sh        # ~20 min (3 clusters via CAPA)
./scripts/phase2/03-attach-vpcs-to-tgw.sh        # ~3 min
./scripts/phase2/04-install-istio.sh              # ~5 min (ambient on cells)
./scripts/phase2/05-verify.sh                     # ~30 sec
\`\`\`

## What This Creates

- **agentic-cp** (10.1.0.0/16): CAPA, cert-manager, Istio sidecar
- **agentic-obs** (10.2.0.0/16): no mesh, ready for VictoriaMetrics/Grafana/Kiali
- **agentic-cell-1** (10.3.0.0/16): Istio ambient, 3 node groups (workload/waypoint/gateway)
- **agentic-cell-2** (10.4.0.0/16): Istio ambient, 3 node groups (workload/waypoint/gateway)
- **Transit Gateway**: all 4 VPCs attached with cross-VPC routing + SG rules

## Next

- **Phase 3**: Deploy platform services to control-plane + observability stack
- **Phase 4**: Deploy cell-level services (kagent, EverMemOS, tenant onboarding)
```

- [ ] **Step 8: chmod +x and commit all scripts**

```bash
chmod +x scripts/phase2/*.sh
git add scripts/phase2/
git commit -m "feat(infra): add Phase 2 deployment scripts and README"
```

---

### Task 6: End-to-end execution

- [ ] **Step 1:** `./scripts/phase2/00-create-control-plane.sh` (~20 min)
- [ ] **Step 2:** `./scripts/phase2/01-create-transit-gateway.sh` (~3 min)
- [ ] **Step 3:** `./scripts/phase2/02-provision-clusters.sh` (~20 min)
- [ ] **Step 4:** `./scripts/phase2/03-attach-vpcs-to-tgw.sh` (~3 min)
- [ ] **Step 5:** `./scripts/phase2/04-install-istio.sh` (~5 min)
- [ ] **Step 6:** `./scripts/phase2/05-verify.sh`
- [ ] **Step 7:** `git commit -m "feat(infra): Phase 2 complete — 4 clusters provisioned with Istio"`
