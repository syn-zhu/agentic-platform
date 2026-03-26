# Phase 1: Foundation — Management Cluster, Transit Gateway, cert-manager

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stand up the management cluster, AWS Transit Gateway, and cert-manager with a shared root CA — the foundation that all other clusters depend on.

**Architecture:** A management EKS cluster (created via eksctl, later self-managed by CAPA) hosts Cluster API with the CAPA provider and cert-manager. A Transit Gateway connects all VPCs. cert-manager issues intermediate CAs that will be distributed to each in-mesh cluster's istiod. This phase produces a working management cluster with the ability to provision additional clusters and issue certificates.

**Tech Stack:** eksctl, AWS CLI, Helm, Cluster API (CAPA), cert-manager, AWS Transit Gateway

**Spec:** `docs/superpowers/specs/2026-03-25-multi-cluster-architecture-design.md`

**Pre-requisites:**
- AWS CLI configured with appropriate credentials (`aws sts get-caller-identity` succeeds)
- `eksctl`, `kubectl`, `helm`, `helmfile` installed
- AWS account in us-east-1

---

## File Structure

```
cluster/
  management/
    cluster.yaml                    # eksctl config for management cluster
    iam-policies/
      capa-controller-policy.json   # IAM policy for CAPA to manage EKS clusters
  transit-gateway/
    create-tgw.sh                   # Creates TGW + VPC attachments
    teardown-tgw.sh                 # Tears down TGW (cleanup)
platform/
  management/
    helmfile.yaml                   # Helmfile for management cluster components
    values/
      cert-manager.yaml             # cert-manager Helm values
      capa.yaml                     # Cluster API + CAPA Helm values
  management-manifests/
    root-ca.yaml                    # cert-manager self-signed root CA ClusterIssuer + Certificate
    intermediate-ca-template.yaml   # Template for per-cluster intermediate CA Certificates
scripts/
  phase1/
    00-create-management-cluster.sh # Creates management EKS cluster
    01-create-transit-gateway.sh    # Creates TGW and attaches management VPC
    02-deploy-cert-manager.sh       # Installs cert-manager + root CA
    03-deploy-capa.sh               # Installs Cluster API + CAPA provider
    04-verify-foundation.sh         # Smoke tests: CAPA healthy, CA issuing, TGW up
```

---

### Task 1: Create management cluster eksctl config

**Files:**
- Create: `cluster/management/cluster.yaml`

- [ ] **Step 1: Create the eksctl config for the management cluster**

```yaml
apiVersion: eksctl.io/v1alpha5
kind: ClusterConfig

metadata:
  name: agentic-mgmt
  region: us-east-1
  version: "1.31"

vpc:
  cidr: "10.0.0.0/16"
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
  - name: management
    instanceType: t3.medium
    minSize: 2
    maxSize: 3
    desiredCapacity: 2
    labels:
      node-role: management
    volumeSize: 30
    volumeType: gp3
    tags:
      role: management
      cluster: agentic-mgmt
```

Write this to `cluster/management/cluster.yaml`.

- [ ] **Step 2: Create the CAPA controller IAM policy**

Create `cluster/management/iam-policies/capa-controller-policy.json`:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "eks:*",
        "ec2:*",
        "autoscaling:*",
        "iam:CreateServiceLinkedRole",
        "iam:PassRole",
        "iam:GetRole",
        "iam:ListAttachedRolePolicies",
        "iam:CreateRole",
        "iam:TagRole",
        "iam:AttachRolePolicy",
        "iam:DetachRolePolicy",
        "iam:DeleteRole",
        "iam:CreateOpenIDConnectProvider",
        "iam:DeleteOpenIDConnectProvider",
        "iam:GetOpenIDConnectProvider",
        "iam:TagOpenIDConnectProvider",
        "iam:CreateInstanceProfile",
        "iam:DeleteInstanceProfile",
        "iam:AddRoleToInstanceProfile",
        "iam:RemoveRoleFromInstanceProfile",
        "ssm:GetParameter",
        "sts:GetCallerIdentity",
        "cloudformation:*",
        "elasticloadbalancing:*"
      ],
      "Resource": "*"
    }
  ]
}
```

- [ ] **Step 3: Commit**

```bash
git add cluster/management/
git commit -m "feat(infra): add management cluster eksctl config and CAPA IAM policy"
```

---

### Task 2: Create management cluster creation script

**Files:**
- Create: `scripts/phase1/00-create-management-cluster.sh`

- [ ] **Step 1: Write the creation script**

```bash
#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

echo "=== Phase 1.0: Creating Management Cluster ==="

# Load environment variables
if [[ -f "$ROOT_DIR/.env" ]]; then
  set -a; source "$ROOT_DIR/.env"; set +a
fi

REGION="us-east-1"
CLUSTER_NAME="agentic-mgmt"

# Resolve AWS Account ID
ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
echo "AWS Account ID: $ACCOUNT_ID"

# Create CAPA controller IAM policy (idempotent)
echo "Creating CAPA controller IAM policy..."
CAPA_POLICY_ARN="arn:aws:iam::${ACCOUNT_ID}:policy/capa-controller"
aws iam create-policy \
  --policy-name capa-controller \
  --policy-document "file://$ROOT_DIR/cluster/management/iam-policies/capa-controller-policy.json" \
  2>/dev/null || echo "  Policy capa-controller already exists, skipping."

# Create EKS cluster
echo "Creating management EKS cluster (this will take ~15-20 minutes)..."
eksctl create cluster -f "$ROOT_DIR/cluster/management/cluster.yaml"

# Update kubeconfig with a unique context name
echo "Updating kubeconfig..."
aws eks update-kubeconfig \
  --name "$CLUSTER_NAME" \
  --region "$REGION" \
  --alias "$CLUSTER_NAME"

# Create gp3 StorageClass
echo "Creating gp3 StorageClass..."
kubectl --context "$CLUSTER_NAME" apply -f - <<'EOF'
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
EOF

echo "=== Management cluster created ==="
echo "Context: $CLUSTER_NAME"
echo "Verify: kubectl --context $CLUSTER_NAME get nodes"
```

Write to `scripts/phase1/00-create-management-cluster.sh` and `chmod +x`.

- [ ] **Step 2: Commit**

```bash
git add scripts/phase1/00-create-management-cluster.sh
git commit -m "feat(infra): add management cluster creation script"
```

---

### Task 3: Create Transit Gateway script

**Files:**
- Create: `cluster/transit-gateway/create-tgw.sh`
- Create: `cluster/transit-gateway/teardown-tgw.sh`

- [ ] **Step 1: Write the TGW creation script**

```bash
#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "=== Creating Transit Gateway ==="

REGION="us-east-1"
TGW_NAME="agentic-platform-tgw"

# Create Transit Gateway
echo "Creating Transit Gateway..."
TGW_ID=$(aws ec2 create-transit-gateway \
  --description "Agentic Platform multi-cluster connectivity" \
  --options "AmazonSideAsn=64512,AutoAcceptSharedAttachments=enable,DefaultRouteTableAssociation=enable,DefaultRouteTablePropagation=enable,DnsSupport=enable" \
  --tag-specifications "ResourceType=transit-gateway,Tags=[{Key=Name,Value=$TGW_NAME},{Key=project,Value=agentic-platform}]" \
  --region "$REGION" \
  --query 'TransitGateway.TransitGatewayId' --output text 2>/dev/null)

if [[ -z "$TGW_ID" || "$TGW_ID" == "None" ]]; then
  echo "  TGW may already exist, looking up..."
  TGW_ID=$(aws ec2 describe-transit-gateways \
    --filters "Name=tag:Name,Values=$TGW_NAME" "Name=state,Values=available,pending" \
    --query 'TransitGateways[0].TransitGatewayId' --output text --region "$REGION")
fi

echo "  Transit Gateway ID: $TGW_ID"

# Wait for TGW to be available
echo "  Waiting for TGW to become available..."
aws ec2 wait transit-gateway-available --transit-gateway-ids "$TGW_ID" --region "$REGION" 2>/dev/null || true

# Attach management cluster VPC
echo "Attaching management cluster VPC..."
MGMT_VPC_ID=$(aws eks describe-cluster --name "agentic-mgmt" --region "$REGION" \
  --query 'cluster.resourcesVpcConfig.vpcId' --output text 2>/dev/null || echo "")

if [[ -n "$MGMT_VPC_ID" && "$MGMT_VPC_ID" != "None" ]]; then
  MGMT_SUBNETS=$(aws ec2 describe-subnets \
    --filters "Name=vpc-id,Values=$MGMT_VPC_ID" "Name=tag:aws:cloudformation:logical-id,Values=SubnetPrivate*" \
    --query 'Subnets[].SubnetId' --output text --region "$REGION")

  aws ec2 create-transit-gateway-vpc-attachment \
    --transit-gateway-id "$TGW_ID" \
    --vpc-id "$MGMT_VPC_ID" \
    --subnet-ids $MGMT_SUBNETS \
    --tag-specifications "ResourceType=transit-gateway-attachment,Tags=[{Key=Name,Value=agentic-mgmt},{Key=project,Value=agentic-platform}]" \
    --region "$REGION" 2>/dev/null || echo "  Management VPC attachment already exists."
  echo "  Management VPC ($MGMT_VPC_ID) attached."
else
  echo "  WARNING: Management cluster not found, skipping VPC attachment."
  echo "  Run 00-create-management-cluster.sh first, then re-run this script."
fi

# Save TGW ID for other scripts
echo "$TGW_ID" > "$SCRIPT_DIR/.tgw-id"
echo ""
echo "=== Transit Gateway ready ==="
echo "TGW ID: $TGW_ID"
echo "Saved to: $SCRIPT_DIR/.tgw-id"
echo ""
echo "To attach additional cluster VPCs later, run:"
echo "  aws ec2 create-transit-gateway-vpc-attachment \\"
echo "    --transit-gateway-id $TGW_ID \\"
echo "    --vpc-id <VPC_ID> --subnet-ids <SUBNET_IDS>"
```

Write to `cluster/transit-gateway/create-tgw.sh` and `chmod +x`.

- [ ] **Step 2: Write the TGW teardown script**

```bash
#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "=== Tearing down Transit Gateway ==="

REGION="us-east-1"
TGW_NAME="agentic-platform-tgw"

TGW_ID=$(aws ec2 describe-transit-gateways \
  --filters "Name=tag:Name,Values=$TGW_NAME" "Name=state,Values=available" \
  --query 'TransitGateways[0].TransitGatewayId' --output text --region "$REGION" 2>/dev/null)

if [[ -z "$TGW_ID" || "$TGW_ID" == "None" ]]; then
  echo "No Transit Gateway found with name $TGW_NAME"
  exit 0
fi

echo "Found TGW: $TGW_ID"

# Delete all attachments first
ATTACHMENTS=$(aws ec2 describe-transit-gateway-vpc-attachments \
  --filters "Name=transit-gateway-id,Values=$TGW_ID" "Name=state,Values=available" \
  --query 'TransitGatewayVpcAttachments[].TransitGatewayAttachmentId' --output text --region "$REGION")

for ATT in $ATTACHMENTS; do
  echo "  Deleting attachment: $ATT"
  aws ec2 delete-transit-gateway-vpc-attachment --transit-gateway-attachment-id "$ATT" --region "$REGION"
done

if [[ -n "$ATTACHMENTS" ]]; then
  echo "  Waiting for attachments to be deleted..."
  sleep 30
fi

# Delete TGW
echo "Deleting Transit Gateway $TGW_ID..."
aws ec2 delete-transit-gateway --transit-gateway-id "$TGW_ID" --region "$REGION"

rm -f "$SCRIPT_DIR/.tgw-id"
echo "=== Transit Gateway deleted ==="
```

Write to `cluster/transit-gateway/teardown-tgw.sh` and `chmod +x`.

- [ ] **Step 3: Commit**

```bash
git add cluster/transit-gateway/
git commit -m "feat(infra): add Transit Gateway create/teardown scripts"
```

---

### Task 4: Create Transit Gateway wrapper script for Phase 1

**Files:**
- Create: `scripts/phase1/01-create-transit-gateway.sh`

- [ ] **Step 1: Write the wrapper script**

```bash
#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

echo "=== Phase 1.1: Transit Gateway Setup ==="

# Load environment variables
if [[ -f "$ROOT_DIR/.env" ]]; then
  set -a; source "$ROOT_DIR/.env"; set +a
fi

# Create TGW and attach management VPC
"$ROOT_DIR/cluster/transit-gateway/create-tgw.sh"

# Add VPC route for TGW
echo ""
echo "Adding VPC route table entries for TGW..."

REGION="us-east-1"
TGW_ID=$(cat "$ROOT_DIR/cluster/transit-gateway/.tgw-id")
MGMT_VPC_ID=$(aws eks describe-cluster --name "agentic-mgmt" --region "$REGION" \
  --query 'cluster.resourcesVpcConfig.vpcId' --output text)

# Get all route tables for the VPC and add routes for other cluster CIDRs
ROUTE_TABLES=$(aws ec2 describe-route-tables \
  --filters "Name=vpc-id,Values=$MGMT_VPC_ID" \
  --query 'RouteTables[].RouteTableId' --output text --region "$REGION")

# CIDRs of future clusters (control-plane, gateway, obs, cell-1, cell-2)
REMOTE_CIDRS=("10.1.0.0/16" "10.2.0.0/16" "10.3.0.0/16" "10.4.0.0/16" "10.5.0.0/16")

for RT in $ROUTE_TABLES; do
  for CIDR in "${REMOTE_CIDRS[@]}"; do
    aws ec2 create-route \
      --route-table-id "$RT" \
      --destination-cidr-block "$CIDR" \
      --transit-gateway-id "$TGW_ID" \
      --region "$REGION" 2>/dev/null || true
  done
done

echo "=== Transit Gateway setup complete ==="
```

Write to `scripts/phase1/01-create-transit-gateway.sh` and `chmod +x`.

- [ ] **Step 2: Commit**

```bash
git add scripts/phase1/01-create-transit-gateway.sh
git commit -m "feat(infra): add Phase 1 TGW setup script with VPC routing"
```

---

### Task 5: Create cert-manager Helm values and root CA manifests

**Files:**
- Create: `platform/management/helmfile.yaml`
- Create: `platform/management/values/cert-manager.yaml`
- Create: `platform/management-manifests/root-ca.yaml`
- Create: `platform/management-manifests/intermediate-ca-template.yaml`

- [ ] **Step 1: Create management helmfile**

```yaml
environments:
  default: {}

---
repositories:
  - name: jetstack
    url: https://charts.jetstack.io

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
```

Write to `platform/management/helmfile.yaml`.

- [ ] **Step 2: Create cert-manager values**

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

Write to `platform/management/values/cert-manager.yaml`.

- [ ] **Step 3: Create root CA manifest**

This creates a self-signed ClusterIssuer, then uses it to issue a root CA certificate, then creates a CA ClusterIssuer backed by the root CA.

```yaml
# Step 1: Bootstrap issuer (self-signed, only used to create the root CA cert)
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: selfsigned-bootstrap
spec:
  selfSigned: {}
---
# Step 2: Root CA certificate (signed by the bootstrap issuer)
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: agentic-platform-root-ca
  namespace: cert-manager
spec:
  isCA: true
  commonName: "Agentic Platform Root CA"
  subject:
    organizations:
      - "Agentic Platform"
  duration: 87600h    # 10 years
  renewBefore: 8760h  # 1 year
  secretName: agentic-platform-root-ca
  privateKey:
    algorithm: ECDSA
    size: 256
  issuerRef:
    name: selfsigned-bootstrap
    kind: ClusterIssuer
    group: cert-manager.io
---
# Step 3: Root CA issuer (used to sign intermediate CAs for each cluster)
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: agentic-platform-root-ca
spec:
  ca:
    secretName: agentic-platform-root-ca
```

Write to `platform/management-manifests/root-ca.yaml`.

- [ ] **Step 4: Create intermediate CA template**

This is a template — during deployment, the script substitutes `CLUSTER_NAME` to generate one Certificate per managed cluster.

```yaml
# Intermediate CA for ${CLUSTER_NAME}
# Issued by the root CA, used by istiod in ${CLUSTER_NAME} to sign SPIFFE workload certs.
# The resulting secret is extracted and copied to ${CLUSTER_NAME}'s istio-system/cacerts.
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: istio-ca-${CLUSTER_NAME}
  namespace: cert-manager
spec:
  isCA: true
  commonName: "Istio CA - ${CLUSTER_NAME}"
  subject:
    organizations:
      - "Agentic Platform"
  duration: 8760h     # 1 year
  renewBefore: 720h   # 30 days
  secretName: istio-ca-${CLUSTER_NAME}
  privateKey:
    algorithm: ECDSA
    size: 256
  issuerRef:
    name: agentic-platform-root-ca
    kind: ClusterIssuer
    group: cert-manager.io
```

Write to `platform/management-manifests/intermediate-ca-template.yaml`.

- [ ] **Step 5: Commit**

```bash
git add platform/management/ platform/management-manifests/
git commit -m "feat(infra): add cert-manager helmfile, root CA, and intermediate CA template"
```

---

### Task 6: Create cert-manager deployment script

**Files:**
- Create: `scripts/phase1/02-deploy-cert-manager.sh`

- [ ] **Step 1: Write the deployment script**

```bash
#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

echo "=== Phase 1.2: Deploying cert-manager + Root CA ==="

CONTEXT="agentic-mgmt"

# Verify we're targeting the management cluster
echo "Verifying cluster context: $CONTEXT"
kubectl --context "$CONTEXT" cluster-info > /dev/null 2>&1 || {
  echo "ERROR: Cannot reach cluster $CONTEXT. Run 00-create-management-cluster.sh first."
  exit 1
}

# Deploy cert-manager via helmfile
echo "Installing cert-manager..."
cd "$ROOT_DIR/platform/management"
KUBECONFIG_CONTEXT="$CONTEXT" helmfile --kube-context "$CONTEXT" sync
cd "$ROOT_DIR"

# Wait for cert-manager webhook to be ready
echo "Waiting for cert-manager webhook..."
kubectl --context "$CONTEXT" -n cert-manager rollout status deployment/cert-manager-webhook --timeout=120s

# Apply root CA manifests
echo "Creating root CA chain..."
kubectl --context "$CONTEXT" apply -f "$ROOT_DIR/platform/management-manifests/root-ca.yaml"

# Wait for root CA certificate to be ready
echo "Waiting for root CA certificate to be issued..."
kubectl --context "$CONTEXT" -n cert-manager wait certificate/agentic-platform-root-ca \
  --for=condition=Ready --timeout=60s

echo ""
echo "=== cert-manager + Root CA deployed ==="
echo "Root CA secret: cert-manager/agentic-platform-root-ca"
echo "ClusterIssuer: agentic-platform-root-ca"
echo ""
echo "To issue an intermediate CA for a cluster:"
echo "  CLUSTER_NAME=control-plane envsubst '\$CLUSTER_NAME' < platform/management-manifests/intermediate-ca-template.yaml | kubectl --context $CONTEXT apply -f -"
```

Write to `scripts/phase1/02-deploy-cert-manager.sh` and `chmod +x`.

- [ ] **Step 2: Commit**

```bash
git add scripts/phase1/02-deploy-cert-manager.sh
git commit -m "feat(infra): add cert-manager deployment script with root CA setup"
```

---

### Task 7: Create CAPA deployment script

**Files:**
- Create: `platform/management/values/capa.yaml`
- Create: `scripts/phase1/03-deploy-capa.sh`

- [ ] **Step 1: Add CAPA to management helmfile**

Add the CAPA Helm repo and release to `platform/management/helmfile.yaml`. The updated file should be:

```yaml
environments:
  default: {}

---
repositories:
  - name: jetstack
    url: https://charts.jetstack.io
  # NOTE: Verify this URL works before implementing:
  #   helm repo add capi <url> && helm search repo capi/cluster-api-operator
  - name: capi
    url: https://kubernetes-sigs.github.io/cluster-api/charts

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
    version: "0.16.0"
    values:
      - values/capa.yaml
    needs:
      - cert-manager/cert-manager
```

- [ ] **Step 2: Create CAPA values**

```yaml
# Cluster API Operator values
resources:
  manager:
    requests:
      cpu: 50m
      memory: 128Mi
    limits:
      memory: 256Mi
```

Write to `platform/management/values/capa.yaml`.

- [ ] **Step 3: Write the CAPA deployment script**

```bash
#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

echo "=== Phase 1.3: Deploying Cluster API + CAPA ==="

CONTEXT="agentic-mgmt"
REGION="us-east-1"

# Verify cluster
kubectl --context "$CONTEXT" cluster-info > /dev/null 2>&1 || {
  echo "ERROR: Cannot reach cluster $CONTEXT."
  exit 1
}

# Verify cert-manager is running (CAPA depends on it)
kubectl --context "$CONTEXT" -n cert-manager get deployment cert-manager-webhook > /dev/null 2>&1 || {
  echo "ERROR: cert-manager not found. Run 02-deploy-cert-manager.sh first."
  exit 1
}

# Set up AWS credentials for CAPA
# CAPA needs AWS credentials to provision EKS clusters
ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
echo "AWS Account ID: $ACCOUNT_ID"

# Create CAPA IAM role and IRSA binding
echo "Setting up CAPA IRSA..."
OIDC_PROVIDER=$(aws eks describe-cluster --name "agentic-mgmt" --region "$REGION" \
  --query 'cluster.identity.oidc.issuer' --output text | sed 's|https://||')

CAPA_ROLE_NAME="agentic-mgmt-capa-controller"
CAPA_POLICY_ARN="arn:aws:iam::${ACCOUNT_ID}:policy/capa-controller"

# Create trust policy for IRSA
cat > /tmp/capa-trust-policy.json <<TRUST
{
  "Version": "2012-10-17",
  "Statement": [
    {
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
    }
  ]
}
TRUST

aws iam create-role \
  --role-name "$CAPA_ROLE_NAME" \
  --assume-role-policy-document "file:///tmp/capa-trust-policy.json" \
  --tags "Key=project,Value=agentic-platform" \
  2>/dev/null || echo "  CAPA IAM role already exists."

aws iam attach-role-policy \
  --role-name "$CAPA_ROLE_NAME" \
  --policy-arn "$CAPA_POLICY_ARN" \
  2>/dev/null || echo "  Policy already attached."

CAPA_ROLE_ARN="arn:aws:iam::${ACCOUNT_ID}:role/${CAPA_ROLE_NAME}"

# Deploy CAPI Operator via helmfile
echo "Installing Cluster API Operator..."
cd "$ROOT_DIR/platform/management"
KUBECONFIG_CONTEXT="$CONTEXT" helmfile --kube-context "$CONTEXT" sync
cd "$ROOT_DIR"

# Create capa-system namespace (operator creates it on reconcile, but CR needs it to exist)
kubectl --context "$CONTEXT" create namespace capa-system --dry-run=client -o yaml \
  | kubectl --context "$CONTEXT" apply -f -

# Create the AWS infrastructure provider with IRSA annotation
# No configSecret needed — IRSA provides credentials via the SA annotation
echo "Creating CAPA infrastructure provider..."
kubectl --context "$CONTEXT" apply -f - <<PROVIDER
apiVersion: operator.cluster.x-k8s.io/v1alpha2
kind: InfrastructureProvider
metadata:
  name: aws
  namespace: capa-system
spec:
  version: v2.7.1
  manager:
    serviceAccountAnnotations:
      eks.amazonaws.com/role-arn: ${CAPA_ROLE_ARN}
PROVIDER

echo "Waiting for CAPA controller to be ready..."
sleep 10
kubectl --context "$CONTEXT" -n capa-system wait deployment --all \
  --for=condition=Available --timeout=180s 2>/dev/null || {
  echo "  CAPA controller still starting, this is normal on first install."
  echo "  Check status with: kubectl --context $CONTEXT -n capa-system get pods"
}

echo ""
echo "=== Cluster API + CAPA deployed ==="
echo "CAPA IAM Role: $CAPA_ROLE_ARN"
echo "Verify: kubectl --context $CONTEXT -n capa-system get pods"
```

Write to `scripts/phase1/03-deploy-capa.sh` and `chmod +x`.

- [ ] **Step 4: Commit**

```bash
git add platform/management/helmfile.yaml platform/management/values/capa.yaml scripts/phase1/03-deploy-capa.sh
git commit -m "feat(infra): add CAPA deployment with IRSA and Helm operator"
```

---

### Task 8: Create verification script

**Files:**
- Create: `scripts/phase1/04-verify-foundation.sh`

- [ ] **Step 1: Write the verification script**

```bash
#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

CONTEXT="agentic-mgmt"
REGION="us-east-1"
PASS=0
FAIL=0

check() {
  local desc="$1"; shift
  if eval "$*" > /dev/null 2>&1; then
    echo "  ✓ $desc"
    ((PASS++))
  else
    echo "  ✗ $desc"
    ((FAIL++))
  fi
}

echo "=== Phase 1 Verification ==="
echo ""

echo "── Management Cluster ──"
check "Cluster reachable" "kubectl --context $CONTEXT cluster-info"
check "Nodes ready" "kubectl --context $CONTEXT get nodes -o jsonpath='{.items[0].status.conditions[?(@.type==\"Ready\")].status}' | grep -q True"
check "gp3 StorageClass exists" "kubectl --context $CONTEXT get sc gp3"

echo ""
echo "── cert-manager ──"
check "cert-manager running" "kubectl --context $CONTEXT -n cert-manager get deployment cert-manager -o jsonpath='{.status.readyReplicas}' | grep -qE '[0-9]+'"
check "cert-manager webhook ready" "kubectl --context $CONTEXT -n cert-manager get deployment cert-manager-webhook -o jsonpath='{.status.readyReplicas}' | grep -qE '[0-9]+'"
check "Root CA ClusterIssuer ready" "kubectl --context $CONTEXT get clusterissuer agentic-platform-root-ca -o jsonpath='{.status.conditions[0].status}' | grep -q True"
check "Root CA Certificate issued" "kubectl --context $CONTEXT -n cert-manager get certificate agentic-platform-root-ca -o jsonpath='{.status.conditions[0].status}' | grep -q True"

echo ""
echo "── Cluster API ──"
check "CAPI operator running" "kubectl --context $CONTEXT -n capi-system get deployment --no-headers | grep -q ."
check "CAPA provider available" "kubectl --context $CONTEXT get infrastructureprovider aws -n capa-system"

echo ""
echo "── Transit Gateway ──"
TGW_ID=$(cat "$ROOT_DIR/cluster/transit-gateway/.tgw-id" 2>/dev/null || echo "")
if [[ -n "$TGW_ID" ]]; then
  check "TGW exists and available" "aws ec2 describe-transit-gateways --transit-gateway-ids $TGW_ID --region $REGION --query 'TransitGateways[0].State' --output text | grep -q available"
  check "Management VPC attached" "aws ec2 describe-transit-gateway-vpc-attachments --filters 'Name=transit-gateway-id,Values=$TGW_ID' --region $REGION --query 'TransitGatewayVpcAttachments[0].State' --output text | grep -q available"
else
  echo "  ✗ TGW ID file not found — run 01-create-transit-gateway.sh"
  ((FAIL+=2))
fi

echo ""
echo "── Test: Issue an intermediate CA ──"
TEST_CERT_NAME="istio-ca-test-cluster"
CLUSTER_NAME="test-cluster" envsubst '$CLUSTER_NAME' \
  < "$ROOT_DIR/platform/management-manifests/intermediate-ca-template.yaml" \
  | kubectl --context "$CONTEXT" apply -f - > /dev/null 2>&1
sleep 5
check "Intermediate CA issued" "kubectl --context $CONTEXT -n cert-manager get certificate $TEST_CERT_NAME -o jsonpath='{.status.conditions[0].status}' | grep -q True"
# Cleanup test cert
kubectl --context "$CONTEXT" -n cert-manager delete certificate "$TEST_CERT_NAME" > /dev/null 2>&1 || true
kubectl --context "$CONTEXT" -n cert-manager delete secret "$TEST_CERT_NAME" > /dev/null 2>&1 || true

echo ""
echo "════════════════════════════"
echo "Results: $PASS passed, $FAIL failed"
if [[ $FAIL -gt 0 ]]; then
  echo "FOUNDATION NOT READY — fix failures above before proceeding to Phase 2"
  exit 1
else
  echo "FOUNDATION READY — proceed to Phase 2"
fi
```

Write to `scripts/phase1/04-verify-foundation.sh` and `chmod +x`.

- [ ] **Step 2: Commit**

```bash
git add scripts/phase1/04-verify-foundation.sh
git commit -m "feat(infra): add Phase 1 verification script"
```

---

### Task 9: Update documentation

**Files:**
- Modify: `docs/superpowers/specs/2026-03-25-multi-cluster-architecture-design.md` (no changes needed — already accurate)
- Create: `scripts/phase1/README.md`

- [ ] **Step 1: Create Phase 1 README**

```markdown
# Phase 1: Foundation

Sets up the management cluster, Transit Gateway, and certificate authority.

## Prerequisites

- AWS CLI configured (`aws sts get-caller-identity` succeeds)
- `eksctl`, `kubectl`, `helm`, `helmfile` installed
- `envsubst` available (part of `gettext`)

## Run Order

```bash
./scripts/phase1/00-create-management-cluster.sh  # ~15-20 min (EKS creation)
./scripts/phase1/01-create-transit-gateway.sh      # ~2-3 min
./scripts/phase1/02-deploy-cert-manager.sh         # ~2-3 min
./scripts/phase1/03-deploy-capa.sh                 # ~3-5 min
./scripts/phase1/04-verify-foundation.sh           # ~30 sec
```

## What This Creates

- **Management EKS cluster** (`agentic-mgmt`) in VPC 10.0.0.0/16
- **Transit Gateway** with management VPC attached and routes to future cluster CIDRs
- **cert-manager** with root CA ClusterIssuer
- **Cluster API + CAPA** ready to provision additional EKS clusters

## Cleanup

```bash
# Delete CAPA resources first (if any managed clusters exist)
kubectl --context agentic-mgmt delete clusters --all -A

# Delete management cluster
eksctl delete cluster --name agentic-mgmt --region us-east-1

# Delete Transit Gateway
./cluster/transit-gateway/teardown-tgw.sh

# Delete IAM resources
aws iam detach-role-policy --role-name agentic-mgmt-capa-controller --policy-arn arn:aws:iam::$(aws sts get-caller-identity --query Account --output text):policy/capa-controller
aws iam delete-role --role-name agentic-mgmt-capa-controller
aws iam delete-policy --policy-arn arn:aws:iam::$(aws sts get-caller-identity --query Account --output text):policy/capa-controller
```

## Phase 2 Prerequisites

Before CAPA can manage remote clusters, cross-VPC security group rules must be configured:
- TCP 443 (kube API) from management VPC to all managed cluster VPCs
- TCP 15008 (HBONE) and TCP 15012 (istiod xDS) between all in-mesh cluster VPCs

These are set up as part of Phase 2 when each managed cluster is created.

## Next

Proceed to **Phase 2: Mesh Core** — creates control-plane, gateway, and observability clusters with Istio 1.29.
```

Write to `scripts/phase1/README.md`.

- [ ] **Step 2: Commit**

```bash
git add scripts/phase1/README.md
git commit -m "docs: add Phase 1 foundation README"
```

---

### Task 10: End-to-end test — run the full Phase 1

**Files:** None (execution only)

- [ ] **Step 1: Run management cluster creation**

```bash
./scripts/phase1/00-create-management-cluster.sh
```

Expected: EKS cluster created, kubeconfig updated, gp3 StorageClass applied. ~15-20 minutes.

- [ ] **Step 2: Run Transit Gateway creation**

```bash
./scripts/phase1/01-create-transit-gateway.sh
```

Expected: TGW created, management VPC attached, routes added. ~2-3 minutes.

- [ ] **Step 3: Run cert-manager deployment**

```bash
./scripts/phase1/02-deploy-cert-manager.sh
```

Expected: cert-manager running, root CA ClusterIssuer ready, root CA Certificate issued.

- [ ] **Step 4: Run CAPA deployment**

```bash
./scripts/phase1/03-deploy-capa.sh
```

Expected: CAPI operator and CAPA provider running in capa-system namespace.

- [ ] **Step 5: Run verification**

```bash
./scripts/phase1/04-verify-foundation.sh
```

Expected: All checks pass, "FOUNDATION READY" message.

- [ ] **Step 6: Final commit**

```bash
git add -A
git commit -m "feat(infra): Phase 1 foundation complete — management cluster, TGW, cert-manager, CAPA"
```
