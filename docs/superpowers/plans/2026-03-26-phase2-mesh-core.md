# Phase 2: Provision Clusters + Per-Cluster Istio

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Use CAPA on the management cluster to provision 4 EKS clusters (control-plane, observability, cell-1, cell-2), connect them to the existing Transit Gateway, and install Istio independently — sidecar mode on control-plane, ambient mode on cells, no mesh on observability.

**Architecture:** CAPA on the management cluster provisions all 4 clusters declaratively. After provisioning, each cluster's VPC is attached to the existing TGW. The control-plane gets Istio in sidecar mode for internal service mesh. Each cell gets an independent Istio ambient mesh. The observability cluster runs no mesh. No multi-cluster mesh — cross-cluster communication uses standard HTTPS over TGW.

**Tech Stack:** CAPA (EKS), Istio 1.29 (Helm), AWS Transit Gateway, helmfile

**Spec:** `docs/superpowers/specs/2026-03-25-multi-cluster-architecture-design.md`

**Pre-requisites:**
- Phase 1 complete: management cluster (`agentic-mgmt`) running with CAPA + cert-manager
- Transit Gateway exists with management VPC attached
- `AWS_PROFILE` set, `eksctl`, `kubectl`, `helm`, `helmfile`, `envsubst` installed

---

## File Structure

```
cluster/
  control-plane/
    cluster.yaml                      # CAPA: Cluster + AWSManagedControlPlane + AWSManagedCluster
    machinepool.yaml                  # CAPA: MachinePool + AWSManagedMachinePool
  observability/
    cluster.yaml                      # CAPA CRs
    machinepool.yaml
  cell-1/
    cluster.yaml                      # CAPA CRs
    machinepool.yaml
  cell-2/
    cluster.yaml                      # CAPA CRs
    machinepool.yaml
  shared/
    storageclass-gp3.yaml             # gp3 StorageClass (applied to all clusters)
platform/
  control-plane/
    helmfile.yaml                     # Istio sidecar for control-plane
    values/
      istiod-sidecar.yaml
  cell/
    helmfile.yaml                     # Istio ambient for cells
    values/
      istiod-ambient.yaml
      istio-cni.yaml
      ztunnel.yaml
scripts/phase2/
    00-provision-clusters.sh          # Apply CAPA CRs, wait for clusters
    01-attach-vpcs-to-tgw.sh          # Attach new VPCs to TGW, add routes + SG rules
    02-install-istio.sh               # Istio on control-plane (sidecar) + cells (ambient)
    03-verify.sh                      # Smoke tests
    README.md
```

---

### Task 1: Create CAPA cluster manifests for all 4 clusters

**Files:**
- Create: `cluster/control-plane/cluster.yaml`
- Create: `cluster/control-plane/machinepool.yaml`
- Create: `cluster/observability/cluster.yaml`
- Create: `cluster/observability/machinepool.yaml`
- Create: `cluster/cell-1/cluster.yaml`
- Create: `cluster/cell-1/machinepool.yaml`
- Create: `cluster/cell-2/cluster.yaml`
- Create: `cluster/cell-2/machinepool.yaml`
- Create: `cluster/shared/storageclass-gp3.yaml`

- [ ] **Step 1: Create control-plane cluster manifests**

`cluster/control-plane/cluster.yaml`:
```yaml
apiVersion: cluster.x-k8s.io/v1beta1
kind: Cluster
metadata:
  name: agentic-cp
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
    name: agentic-cp
  controlPlaneRef:
    apiVersion: controlplane.cluster.x-k8s.io/v1beta2
    kind: AWSManagedControlPlane
    name: agentic-cp
---
apiVersion: infrastructure.cluster.x-k8s.io/v1beta2
kind: AWSManagedCluster
metadata:
  name: agentic-cp
  namespace: default
spec: {}
---
apiVersion: controlplane.cluster.x-k8s.io/v1beta2
kind: AWSManagedControlPlane
metadata:
  name: agentic-cp
  namespace: default
spec:
  eksClusterName: agentic-cp
  region: us-east-1
  version: v1.31.0
  network:
    vpc:
      cidrBlock: "10.1.0.0/16"
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
    cluster-type: control-plane
```

`cluster/control-plane/machinepool.yaml`:
```yaml
apiVersion: cluster.x-k8s.io/v1beta1
kind: MachinePool
metadata:
  name: agentic-cp-platform
  namespace: default
spec:
  clusterName: agentic-cp
  replicas: 2
  template:
    spec:
      clusterName: agentic-cp
      bootstrap:
        dataSecretName: ""
      infrastructureRef:
        apiVersion: infrastructure.cluster.x-k8s.io/v1beta2
        kind: AWSManagedMachinePool
        name: agentic-cp-platform
---
apiVersion: infrastructure.cluster.x-k8s.io/v1beta2
kind: AWSManagedMachinePool
metadata:
  name: agentic-cp-platform
  namespace: default
spec:
  instanceType: t3.large
  scaling:
    minSize: 2
    maxSize: 4
  diskSize: 30
  labels:
    node-role: platform
  capacityType: onDemand
  additionalTags:
    project: agentic-platform
    role: platform
```

- [ ] **Step 2: Create observability cluster manifests**

`cluster/observability/cluster.yaml` — same structure as control-plane but:
- Names: `agentic-obs`
- VPC CIDR: `10.2.0.0/16`
- Tag: `cluster-type: observability`

`cluster/observability/machinepool.yaml`:
- Pool name: `agentic-obs-pool`, `t3.large`, min 2 / max 3, label `node-role: obs`

- [ ] **Step 3: Create cell-1 cluster manifests**

`cluster/cell-1/cluster.yaml` — same structure but:
- Names: `agentic-cell-1`
- VPC CIDR: `10.3.0.0/16`
- Tag: `cluster-type: cell`

`cluster/cell-1/machinepool.yaml` — three machine pools:

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

- [ ] **Step 4: Create cell-2 cluster manifests**

Identical to cell-1 but with names `agentic-cell-2` and VPC CIDR `10.4.0.0/16`.

- [ ] **Step 5: Create shared gp3 StorageClass**

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

- [ ] **Step 6: Commit**

```bash
git add cluster/control-plane/ cluster/observability/ cluster/cell-1/ cluster/cell-2/ cluster/shared/
git commit -m "feat(infra): add CAPA cluster manifests for control-plane, observability, cell-1, cell-2"
```

---

### Task 2: Create Istio helmfiles

**Files:**
- Create: `platform/control-plane/helmfile.yaml`
- Create: `platform/control-plane/values/istiod-sidecar.yaml`
- Create: `platform/cell/helmfile.yaml`
- Create: `platform/cell/values/istiod-ambient.yaml`
- Create: `platform/cell/values/istio-cni.yaml`
- Create: `platform/cell/values/ztunnel.yaml`

- [ ] **Step 1: Create control-plane Istio helmfile**

`platform/control-plane/helmfile.yaml`:
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
      - values/istiod-sidecar.yaml
    needs:
      - istio-system/istio-base
```

`platform/control-plane/values/istiod-sidecar.yaml`:
```yaml
# Istio sidecar mode — internal control-plane service mesh only
profile: default
```

- [ ] **Step 2: Create cell Istio helmfile**

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
git add platform/control-plane/ platform/cell/
git commit -m "feat(istio): add helmfiles for control-plane (sidecar) and cell (ambient) Istio"
```

---

### Task 3: Create deployment scripts

**Files:**
- Create: `scripts/phase2/00-provision-clusters.sh`
- Create: `scripts/phase2/01-attach-vpcs-to-tgw.sh`
- Create: `scripts/phase2/02-install-istio.sh`
- Create: `scripts/phase2/03-verify.sh`
- Create: `scripts/phase2/README.md`

- [ ] **Step 1: Write 00-provision-clusters.sh**

Applies CAPA CRs to the management cluster, waits for all 4 clusters to become ready, updates kubeconfig, applies gp3 StorageClass.

```bash
#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

echo "=== Phase 2.0: Provisioning Clusters via CAPA ==="

MGMT_CTX="agentic-mgmt"
REGION="us-east-1"
CLUSTER_DIRS=("control-plane" "observability" "cell-1" "cell-2")
EKS_NAMES=("agentic-cp" "agentic-obs" "agentic-cell-1" "agentic-cell-2")

# Verify management cluster
kubectl --context "$MGMT_CTX" cluster-info > /dev/null 2>&1 || {
  echo "ERROR: Cannot reach management cluster. Is Phase 1 complete?"
  exit 1
}

# Apply CAPA CRs for each cluster
for DIR in "${CLUSTER_DIRS[@]}"; do
  echo ""
  echo "── Provisioning: $DIR ──"
  kubectl --context "$MGMT_CTX" apply -f "$ROOT_DIR/cluster/$DIR/cluster.yaml"
  kubectl --context "$MGMT_CTX" apply -f "$ROOT_DIR/cluster/$DIR/machinepool.yaml"
done

# Wait for clusters to be ready
echo ""
echo "Waiting for clusters to be provisioned (this may take 15-20 minutes)..."
for NAME in "${EKS_NAMES[@]}"; do
  echo "  Waiting for $NAME..."
  kubectl --context "$MGMT_CTX" -n default wait cluster "$NAME" \
    --for=condition=Ready --timeout=1200s 2>/dev/null || {
    echo "  WARNING: $NAME not ready yet. Check: kubectl --context $MGMT_CTX get cluster $NAME"
  }
done

# Update kubeconfig for each cluster
echo ""
echo "Updating kubeconfig..."
for NAME in "${EKS_NAMES[@]}"; do
  aws eks update-kubeconfig --name "$NAME" --region "$REGION" --alias "$NAME" 2>/dev/null || \
    echo "  $NAME kubeconfig not yet available"
done

# Apply gp3 StorageClass on each cluster
for NAME in "${EKS_NAMES[@]}"; do
  echo "Creating gp3 StorageClass on $NAME..."
  kubectl --context "$NAME" apply -f "$ROOT_DIR/cluster/shared/storageclass-gp3.yaml" 2>/dev/null || true
done

echo ""
echo "=== Clusters provisioned: ${EKS_NAMES[*]} ==="
```

- [ ] **Step 2: Write 01-attach-vpcs-to-tgw.sh**

Attaches the 4 new VPCs to the existing TGW, adds cross-VPC routes and security group rules.

```bash
#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

echo "=== Phase 2.1: Attaching VPCs to Transit Gateway ==="

REGION="us-east-1"
TGW_ID=$(cat "$ROOT_DIR/cluster/transit-gateway/.tgw-id")
EKS_NAMES=("agentic-cp" "agentic-obs" "agentic-cell-1" "agentic-cell-2")
ALL_CIDRS=("10.0.0.0/16" "10.1.0.0/16" "10.2.0.0/16" "10.3.0.0/16" "10.4.0.0/16")

for EKS_NAME in "${EKS_NAMES[@]}"; do
  echo ""
  echo "── Attaching: $EKS_NAME ──"
  VPC_ID=$(aws eks describe-cluster --name "$EKS_NAME" --region "$REGION" \
    --query 'cluster.resourcesVpcConfig.vpcId' --output text 2>/dev/null || echo "")
  [[ -z "$VPC_ID" || "$VPC_ID" == "None" ]] && echo "  Skipping — not found." && continue

  # Get private subnets (array to handle tab-separated output)
  SUBNETS=($(aws ec2 describe-subnets \
    --filters "Name=vpc-id,Values=$VPC_ID" "Name=map-public-ip-on-launch,Values=false" \
    --query 'Subnets[].SubnetId' --output text --region "$REGION"))
  echo "  VPC: $VPC_ID, Subnets: ${SUBNETS[*]}"

  # Create TGW attachment
  aws ec2 create-transit-gateway-vpc-attachment \
    --transit-gateway-id "$TGW_ID" --vpc-id "$VPC_ID" \
    --subnet-ids "${SUBNETS[@]}" \
    --tag-specifications "ResourceType=transit-gateway-attachment,Tags=[{Key=Name,Value=$EKS_NAME},{Key=project,Value=agentic-platform}]" \
    --region "$REGION" 2>/dev/null || echo "  Attachment already exists."

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

  # Add SG rule for HTTPS (443) from all VPCs
  SG_ID=$(aws eks describe-cluster --name "$EKS_NAME" --region "$REGION" \
    --query 'cluster.resourcesVpcConfig.clusterSecurityGroupId' --output text)
  for CIDR in "${ALL_CIDRS[@]}"; do
    aws ec2 authorize-security-group-ingress \
      --group-id "$SG_ID" --protocol tcp --port 443 --cidr "$CIDR" \
      --region "$REGION" 2>/dev/null || true
  done
  echo "  Routes + SG rules added."
done

echo ""
echo "=== VPC attachments complete ==="
```

- [ ] **Step 3: Write 02-install-istio.sh**

Installs Istio sidecar on control-plane and ambient on each cell.

```bash
#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

echo "=== Phase 2.2: Installing Istio ==="

# ── Control-plane: Istio sidecar mode ──
echo ""
echo "── Installing Istio sidecar on agentic-cp ──"

kubectl --context "agentic-cp" get crd gateways.gateway.networking.k8s.io > /dev/null 2>&1 || \
  kubectl --context "agentic-cp" apply --server-side \
    -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.4.0/experimental-install.yaml

cd "$ROOT_DIR/platform/control-plane"
helmfile --kube-context "agentic-cp" sync
cd "$ROOT_DIR"

kubectl --context "agentic-cp" -n istio-system rollout status deployment/istiod --timeout=120s
echo "  ✓ Istio sidecar installed on agentic-cp"

# ── Cells: Istio ambient mode ──
CELL_CLUSTERS=("agentic-cell-1" "agentic-cell-2")

for CLUSTER in "${CELL_CLUSTERS[@]}"; do
  echo ""
  echo "── Installing Istio ambient on $CLUSTER ──"

  kubectl --context "$CLUSTER" get crd gateways.gateway.networking.k8s.io > /dev/null 2>&1 || \
    kubectl --context "$CLUSTER" apply --server-side \
      -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.4.0/experimental-install.yaml

  cd "$ROOT_DIR/platform/cell"
  helmfile --kube-context "$CLUSTER" sync
  cd "$ROOT_DIR"

  kubectl --context "$CLUSTER" -n istio-system rollout status deployment/istiod --timeout=120s
  echo "  ✓ Istio ambient installed on $CLUSTER"
done

echo ""
echo "=== Istio installed ==="
echo "  agentic-cp: sidecar mode"
echo "  agentic-cell-1, agentic-cell-2: ambient mode"
echo "  agentic-obs: no mesh (intentional)"
```

- [ ] **Step 4: Write 03-verify.sh**

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
check "Nodes ready" "kubectl --context agentic-cp get nodes -o jsonpath='{.items[0].status.conditions[?(@.type==\"Ready\")].status}' | grep -q True"
check "istiod running (sidecar)" "kubectl --context agentic-cp -n istio-system get deployment istiod -o jsonpath='{.status.readyReplicas}' | grep -qE '[0-9]+'"
check "gp3 StorageClass" "kubectl --context agentic-cp get sc gp3"

echo ""
echo "── Observability (agentic-obs) ──"
check "Cluster reachable" "kubectl --context agentic-obs cluster-info"
check "Nodes ready" "kubectl --context agentic-obs get nodes -o jsonpath='{.items[0].status.conditions[?(@.type==\"Ready\")].status}' | grep -q True"
check "gp3 StorageClass" "kubectl --context agentic-obs get sc gp3"

for CELL in agentic-cell-1 agentic-cell-2; do
  echo ""
  echo "── $CELL ──"
  check "Cluster reachable" "kubectl --context $CELL cluster-info"
  check "Nodes ready" "kubectl --context $CELL get nodes -o jsonpath='{.items[0].status.conditions[?(@.type==\"Ready\")].status}' | grep -q True"
  check "istiod running (ambient)" "kubectl --context $CELL -n istio-system get deployment istiod -o jsonpath='{.status.readyReplicas}' | grep -qE '[0-9]+'"
  check "ztunnel running" "kubectl --context $CELL -n istio-system get daemonset ztunnel -o jsonpath='{.status.numberReady}' | grep -qE '[0-9]+'"
  check "istio-cni running" "kubectl --context $CELL -n istio-system get daemonset istio-cni-node -o jsonpath='{.status.numberReady}' | grep -qE '[0-9]+'"
  check "gp3 StorageClass" "kubectl --context $CELL get sc gp3"
done

echo ""
echo "── Transit Gateway ──"
TGW_ID=$(cat "$ROOT_DIR/cluster/transit-gateway/.tgw-id" 2>/dev/null || echo "")
if [[ -n "$TGW_ID" ]]; then
  ATTACHMENTS=$(aws ec2 describe-transit-gateway-vpc-attachments \
    --filters "Name=transit-gateway-id,Values=$TGW_ID" "Name=state,Values=available" \
    --region "$REGION" --query 'length(TransitGatewayVpcAttachments)' --output text)
  check "TGW has 5 VPC attachments (mgmt+cp+obs+cell1+cell2)" "test $ATTACHMENTS -ge 5"
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
  echo "PHASE 2 READY — proceed to Phase 3"
fi
```

- [ ] **Step 5: Write README.md**

```markdown
# Phase 2: Provision Clusters + Per-Cluster Istio

## Prerequisites

- Phase 1 complete: management cluster running with CAPA
- `AWS_PROFILE` set, `kubectl`, `helm`, `helmfile` installed

## Run Order

\`\`\`bash
./scripts/phase2/00-provision-clusters.sh     # ~20 min (4 clusters via CAPA)
./scripts/phase2/01-attach-vpcs-to-tgw.sh     # ~5 min
./scripts/phase2/02-install-istio.sh           # ~5 min
./scripts/phase2/03-verify.sh                  # ~30 sec
\`\`\`

## What This Creates

- **agentic-cp** (10.1.0.0/16): Istio sidecar, ready for platform services
- **agentic-obs** (10.2.0.0/16): no mesh, ready for VictoriaMetrics/Grafana/Kiali
- **agentic-cell-1** (10.3.0.0/16): Istio ambient, 3 node groups (workload/waypoint/gateway)
- **agentic-cell-2** (10.4.0.0/16): Istio ambient, 3 node groups
- **Transit Gateway**: all 5 VPCs attached with cross-VPC routing + SG rules

## Next

- **Phase 3**: Deploy platform services to control-plane + observability stack
- **Phase 4**: Deploy cell services (kagent, EverMemOS, tenant onboarding)
```

- [ ] **Step 6: chmod +x and commit**

```bash
chmod +x scripts/phase2/*.sh
git add scripts/phase2/
git commit -m "feat(infra): add Phase 2 deployment scripts and README"
```

---

### Task 4: End-to-end execution

- [ ] **Step 1:** `./scripts/phase2/00-provision-clusters.sh` (~20 min)
- [ ] **Step 2:** `./scripts/phase2/01-attach-vpcs-to-tgw.sh` (~5 min)
- [ ] **Step 3:** `./scripts/phase2/02-install-istio.sh` (~5 min)
- [ ] **Step 4:** `./scripts/phase2/03-verify.sh`
- [ ] **Step 5:** Commit: `git commit -m "feat(infra): Phase 2 complete — 4 clusters provisioned with Istio"`
