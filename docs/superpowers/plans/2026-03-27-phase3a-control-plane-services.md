# Phase 3a: Control-Plane Services

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deploy platform services (Keycloak, OpenFGA, AgentRegistry, Langfuse+ClickHouse) to the control-plane cluster with AWS backing resources, Istio ingress, and telemetry export.

**Architecture:** New RDS PostgreSQL and ElastiCache Redis instances in the control-plane VPC back Keycloak, OpenFGA, AgentRegistry, and Langfuse. A standard Istio ingress gateway exposes platform APIs. OTel Agent+Gateway collect traces for Langfuse (local). vmagent exports metrics to VictoriaMetrics (obs cluster, future Phase 3b). Existing Helm values and manifests are adapted from the single-cluster setup with updated endpoints.

**Tech Stack:** Helm/Helmfile, AWS CLI (RDS, ElastiCache, S3), Istio Gateway API, OTel Collector

**Spec:** `docs/superpowers/specs/2026-03-25-multi-cluster-architecture-design.md`
**Phase 3 scope:** `memory/project_phase3_scope.md`

**Pre-requisites:**
- Phase 2 complete (5 clusters running, Istio on cp + cells)
- `AWS_PROFILE=mms-test`, `kubectl` contexts: `agentic-cp`, `agentic-obs`, `agentic-cell-*`
- Control-plane VPC: `10.1.0.0/16`, cluster: `agentic-cp`

---

## File Structure

```
scripts/phase3a/
  00-create-aws-resources.sh        # RDS, ElastiCache, S3 in control-plane VPC
  01-create-secrets.sh              # K8s secrets from AWS outputs
  02-deploy-control-plane.sh        # helmfile sync for cp services
  03-configure-keycloak.sh          # Realm import, org creation
  04-verify.sh                      # Smoke tests
  README.md
platform/
  control-plane/
    helmfile.yaml                   # Extended with all cp services (already has Istio)
    values/
      istiod-sidecar.yaml           # (exists from Phase 2)
      keycloak.yaml                 # Adapted from existing values
      openfga.yaml                  # Adapted
      langfuse.yaml                 # Adapted
      clickhouse.yaml               # Adapted
  control-plane-manifests/
    namespaces.yaml                 # cp cluster namespaces
    agentregistry.yaml              # Adapted from existing manifest
    otel-collector.yaml             # OTel Agent+Gateway config
    istio-ingress-gateway.yaml      # Istio Gateway + HTTPRoutes for platform APIs
    capi-providers.yaml             # (exists from Phase 1)
    capa-variables-secret.yaml      # (exists from Phase 1)
cluster/
  control-plane/
    aws-resources/
      rds-config.json               # RDS parameters (committed source of truth)
      elasticache-config.json        # ElastiCache parameters
```

---

### Task 1: Create AWS resource configuration and creation script

**Files:**
- Create: `cluster/control-plane/aws-resources/rds-config.json`
- Create: `cluster/control-plane/aws-resources/elasticache-config.json`
- Create: `scripts/phase3a/00-create-aws-resources.sh`

The script creates RDS PostgreSQL, ElastiCache Redis, and S3 bucket in the control-plane VPC. Configuration parameters are read from committed JSON files (declarative source of truth). Script discovers VPC/subnet info from the EKS cluster.

- [ ] **Step 1: Create RDS configuration**

`cluster/control-plane/aws-resources/rds-config.json`:
```json
{
  "instanceIdentifier": "agentic-cp-postgres",
  "instanceClass": "db.t4g.medium",
  "engine": "postgres",
  "engineVersion": "17",
  "allocatedStorage": 20,
  "storageType": "gp3",
  "masterUsername": "postgres",
  "databases": ["langfuse", "keycloak", "openfga", "agentregistry"],
  "backupRetentionPeriod": 1,
  "multiAZ": false,
  "storageEncrypted": true,
  "tags": {
    "project": "agentic-platform",
    "cluster": "agentic-cp"
  }
}
```

- [ ] **Step 2: Create ElastiCache configuration**

`cluster/control-plane/aws-resources/elasticache-config.json`:
```json
{
  "replicationGroupId": "agentic-cp-redis",
  "nodeType": "cache.t4g.small",
  "numCacheClusters": 1,
  "transitEncryptionEnabled": true,
  "tags": {
    "project": "agentic-platform",
    "cluster": "agentic-cp"
  }
}
```

- [ ] **Step 3: Create the AWS resource creation script**

`scripts/phase3a/00-create-aws-resources.sh` — adapted from the existing `scripts/01-create-aws-resources.sh` but targeting the control-plane VPC (`agentic-cp` cluster). Uses committed config files for parameters. Generates passwords and saves outputs to `.env.cp` for subsequent scripts.

Key differences from the old script:
- Discovers VPC from `agentic-cp` cluster (not `agentic-platform`)
- Uses `map-public-ip-on-launch=false` filter for private subnets (not CloudFormation tags)
- Reads config from `cluster/control-plane/aws-resources/*.json`
- Creates S3 bucket `agentic-platform-langfuse-cp`
- Also creates additional databases (keycloak, openfga, agentregistry) on the same RDS instance
- Saves outputs to `$ROOT_DIR/.env.cp`
- Security group allows inbound 5432/6379 from ALL cluster VPC CIDRs (10.0-10.4.0.0/16) for cross-cluster access via TGW

- [ ] **Step 4: Commit**

```bash
git add cluster/control-plane/aws-resources/ scripts/phase3a/00-create-aws-resources.sh
git commit -m "feat(infra): add AWS resource configs and creation script for control-plane"
```

---

### Task 2: Create secrets script

**Files:**
- Create: `scripts/phase3a/01-create-secrets.sh`

Reads `.env.cp` (outputs from Task 1) and creates Kubernetes secrets on the control-plane cluster. Secrets include: RDS credentials, Redis auth token, Langfuse keys, S3 config, Keycloak admin password.

- [ ] **Step 1: Write the secrets script**

The script creates secrets in the appropriate namespaces on `agentic-cp`:
- `langfuse/langfuse-db-credentials` — RDS password
- `langfuse/langfuse-secrets` — NEXTAUTH_SECRET, ENCRYPTION_KEY, SALT, S3 config
- `keycloak/keycloak-db-credentials` — RDS password for keycloak DB
- `openfga/openfga-db-credentials` — RDS password for openfga DB
- `agentregistry/agentregistry-db-credentials` — RDS password + connection string

- [ ] **Step 2: Commit**

```bash
git add scripts/phase3a/01-create-secrets.sh
git commit -m "feat(infra): add secrets creation script for control-plane services"
```

---

### Task 3: Create control-plane namespaces manifest

**Files:**
- Create: `platform/control-plane-manifests/namespaces.yaml`

- [ ] **Step 1: Create namespaces**

```yaml
# Control-plane cluster namespaces
# Namespaces with istio-injection=enabled get sidecar proxies
apiVersion: v1
kind: Namespace
metadata:
  name: keycloak
  labels:
    istio-injection: enabled
---
apiVersion: v1
kind: Namespace
metadata:
  name: openfga
  labels:
    istio-injection: enabled
---
apiVersion: v1
kind: Namespace
metadata:
  name: agentregistry
  labels:
    istio-injection: enabled
---
apiVersion: v1
kind: Namespace
metadata:
  name: langfuse
  labels:
    istio-injection: enabled
---
apiVersion: v1
kind: Namespace
metadata:
  name: otel-system
---
apiVersion: v1
kind: Namespace
metadata:
  name: monitoring
```

- [ ] **Step 2: Commit**

```bash
git add platform/control-plane-manifests/namespaces.yaml
git commit -m "feat(infra): add control-plane cluster namespace manifests"
```

---

### Task 4: Create control-plane helmfile with all services

**Files:**
- Modify: `platform/control-plane/helmfile.yaml` (extend with service releases)
- Create: `platform/control-plane/values/keycloak.yaml`
- Create: `platform/control-plane/values/openfga.yaml`
- Create: `platform/control-plane/values/langfuse.yaml`
- Create: `platform/control-plane/values/clickhouse.yaml`

- [ ] **Step 1: Extend control-plane helmfile**

Add repositories and releases for Keycloak, OpenFGA, Langfuse, ClickHouse to the existing helmfile (which already has Istio base + istiod).

New repositories: langfuse, bitnami, codecentric, openfga
New releases: keycloak, openfga, clickhouse, langfuse

- [ ] **Step 2: Adapt Keycloak values**

Copy `platform/values/keycloak.yaml` to `platform/control-plane/values/keycloak.yaml`. Update:
- RDS endpoint placeholder (will be substituted by deploy script from `.env.cp`)
- Remove scheduling to platform node group (cp has untainted nodes)
- Keep all Keycloak config (realm, orgs, token exchange, etc.)

- [ ] **Step 3: Adapt OpenFGA values**

Copy and update:
- RDS endpoint for openfga DB
- Remove kube-prometheus-stack dependency (Prometheus is in obs cluster now)
- Keep OTEL export config (will point to local OTel collector)

- [ ] **Step 4: Adapt Langfuse values**

Copy and update:
- RDS endpoint for langfuse DB
- Redis endpoint
- S3 bucket name (`agentic-platform-langfuse-cp`)
- ClickHouse connection (local, same cluster)
- OTEL receiver config

- [ ] **Step 5: Adapt ClickHouse values**

Copy from existing. Update:
- Remove Istio HBONE NetworkPolicy rule (cp uses sidecar, not ambient)
- Keep HA config (2 replicas, 3 keeper)

- [ ] **Step 6: Commit**

```bash
git add platform/control-plane/
git commit -m "feat(cp): add helmfile releases and values for Keycloak, OpenFGA, Langfuse, ClickHouse"
```

---

### Task 5: Create AgentRegistry manifest for control-plane

**Files:**
- Create: `platform/control-plane-manifests/agentregistry.yaml`

- [ ] **Step 1: Adapt AgentRegistry manifest**

Copy `platform/manifests/agentregistry.yaml` to `platform/control-plane-manifests/agentregistry.yaml`. Update:
- Namespace remains `agentregistry`
- RDS connection string will use placeholder (substituted by deploy script)
- Remove references to old cluster image tags if needed
- Keep all existing config (REST API port 8080, MCP port 8090, OIDC auth)

- [ ] **Step 2: Commit**

```bash
git add platform/control-plane-manifests/agentregistry.yaml
git commit -m "feat(cp): add AgentRegistry manifest for control-plane cluster"
```

---

### Task 6: Create OTel collector manifest for control-plane

**Files:**
- Create: `platform/control-plane-manifests/otel-collector.yaml`

- [ ] **Step 1: Create OTel config**

Adapted from existing `platform/manifests/otel-collector.yaml`. The control-plane OTel collector:
- Receives OTLP gRPC (4317) and HTTP (4318) from local services
- Adds `k8s.cluster.name: agentic-cp` resource attribute
- Exports to Langfuse (local: `http://langfuse-web.langfuse.svc.cluster.local:3000/api/public/otel`)
- Runs in `otel-system` namespace

- [ ] **Step 2: Commit**

```bash
git add platform/control-plane-manifests/otel-collector.yaml
git commit -m "feat(cp): add OTel collector manifest for control-plane"
```

---

### Task 7: Create Istio ingress gateway for platform APIs

**Files:**
- Create: `platform/control-plane-manifests/istio-ingress-gateway.yaml`

- [ ] **Step 1: Create Gateway + HTTPRoutes**

Standard Istio Gateway (not agentgateway) with HTTPRoutes for platform services:

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: platform-gateway
  namespace: istio-system
spec:
  gatewayClassName: istio
  listeners:
    - name: http
      port: 80
      protocol: HTTP
---
# HTTPRoutes for each platform service
# /auth/* → keycloak
# /openfga/* → openfga
# /registry/* → agentregistry
# /langfuse/* → langfuse-web (OTLP ingest + UI)
```

Each HTTPRoute matches a path prefix and routes to the corresponding service. The Gateway gets a LoadBalancer Service (internal NLB via annotation for TGW access).

- [ ] **Step 2: Commit**

```bash
git add platform/control-plane-manifests/istio-ingress-gateway.yaml
git commit -m "feat(cp): add Istio ingress gateway with HTTPRoutes for platform APIs"
```

---

### Task 8: Create deployment script

**Files:**
- Create: `scripts/phase3a/02-deploy-control-plane.sh`

- [ ] **Step 1: Write the deployment script**

The script:
1. Reads `.env.cp` for AWS endpoints
2. Substitutes endpoints into Helm values (using `envsubst` on committed templates)
3. Applies namespaces
4. Runs `helmfile sync` on the control-plane helmfile
5. Applies additional manifests (AgentRegistry, OTel, ingress gateway)
6. Waits for all deployments to be ready

- [ ] **Step 2: Commit**

```bash
git add scripts/phase3a/02-deploy-control-plane.sh
git commit -m "feat(cp): add control-plane deployment script"
```

---

### Task 9: Create Keycloak configuration script

**Files:**
- Create: `scripts/phase3a/03-configure-keycloak.sh`

- [ ] **Step 1: Write Keycloak config script**

Adapted from existing `scripts/06-configure-keycloak.sh`:
- Imports the `agents` realm from `platform/manifests/keycloak-agents-realm.json` (or creates it)
- Enables Organizations
- Creates the `organization` client scope
- Creates initial org (e.g., `acme` for testing)

Keycloak is accessed via `kubectl port-forward` to the keycloak pod on `agentic-cp`.

- [ ] **Step 2: Commit**

```bash
git add scripts/phase3a/03-configure-keycloak.sh
git commit -m "feat(cp): add Keycloak configuration script"
```

---

### Task 10: Create verification script and README

**Files:**
- Create: `scripts/phase3a/04-verify.sh`
- Create: `scripts/phase3a/README.md`

- [ ] **Step 1: Write verification script**

Checks:
- All deployments healthy (keycloak, openfga, langfuse, clickhouse, agentregistry, otel-collector)
- RDS reachable (via pod exec or port-forward)
- Keycloak OIDC discovery endpoint responds
- AgentRegistry health check responds
- Langfuse health check responds
- Istio ingress gateway has external IP/hostname
- OTel collector receiving traces (check logs)

- [ ] **Step 2: Write README**

Documents run order, prerequisites, what gets created, cleanup steps.

- [ ] **Step 3: Commit**

```bash
git add scripts/phase3a/
git commit -m "feat(cp): add Phase 3a verification script and README"
```

---

### Task 11: End-to-end execution

- [ ] **Step 1:** `./scripts/phase3a/00-create-aws-resources.sh` (~10-15 min for RDS creation)
- [ ] **Step 2:** `./scripts/phase3a/01-create-secrets.sh` (~30 sec)
- [ ] **Step 3:** `./scripts/phase3a/02-deploy-control-plane.sh` (~5-10 min)
- [ ] **Step 4:** `./scripts/phase3a/03-configure-keycloak.sh` (~1 min)
- [ ] **Step 5:** `./scripts/phase3a/04-verify.sh`
- [ ] **Step 6:** Commit: `git commit -m "feat(cp): Phase 3a control-plane services deployed"`
