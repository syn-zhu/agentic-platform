# Phase 3a: Control-Plane Services

Deploys Keycloak, OpenFGA, Langfuse (+ ClickHouse), AgentRegistry, and OTel Collector to the `agentic-cp` cluster with AWS-managed backing resources (RDS PostgreSQL 17, ElastiCache Redis, S3).

**Cluster:** `agentic-cp`
**Backing:** AWS RDS (`agentic-cp-postgres`), ElastiCache (`agentic-cp-redis`), S3 (`agentic-platform-langfuse-cp`)
**Ingress:** Istio gateway `platform-gateway` in `istio-system`

---

## Prerequisites

- Phase 2 complete (5 clusters running, Istio on cp + cells, TGW attached)
- `AWS_PROFILE=mms-test` (access to AWS account 268558157000)
- `kubectl` contexts: `agentic-cp` (and optionally `agentic-obs`, `agentic-cell-*`)
- Tools: `kubectl`, `helmfile`, `helm`, `aws`, `curl`, `python3`, `openssl`

---

## Run Order

```bash
cd ~/agentic-platform/scripts/phase3a

# 1. Create AWS resources (RDS, ElastiCache, S3) — ~10-15 min for RDS
./00-create-aws-resources.sh

# 2. Create Kubernetes secrets on agentic-cp from .env.cp outputs
./01-create-secrets.sh

# 3. Run helmfile sync and apply additional manifests
./02-deploy-control-plane.sh

# 4. Configure Keycloak: import platform realm, enable orgs, create acme org
./03-configure-keycloak.sh

# 5. Verify all services healthy
./04-verify.sh
```

---

## What Gets Created

### AWS Resources (`00-create-aws-resources.sh`)
- RDS PostgreSQL 17 instance `agentic-cp-postgres` with databases: `keycloak`, `openfga`, `langfuse`, `agentregistry`
- ElastiCache Redis `agentic-cp-redis` (TLS, auth token)
- S3 bucket `agentic-platform-langfuse-cp`
- Security group allowing inbound 5432/6379 from all cluster VPC CIDRs (10.0–10.4.0.0/16) for cross-cluster access via TGW
- Outputs written to `$ROOT_DIR/.env.cp` (do not commit)

### Kubernetes Secrets (`01-create-secrets.sh`)
| Namespace | Secret | Contents |
|-----------|--------|----------|
| `langfuse` | `langfuse-db-credentials` | RDS password + DATABASE_URL |
| `langfuse` | `langfuse-secrets` | NEXTAUTH_SECRET, ENCRYPTION_KEY, SALT, S3 config |
| `keycloak` | `keycloak-db-credentials` | RDS password for keycloak DB |
| `openfga` | `openfga-db-credentials` | RDS password + connection URI |
| `agentregistry` | `agentregistry-db-credentials` | AGENT_REGISTRY_DATABASE_URL |

### Helm Releases (`02-deploy-control-plane.sh` via helmfile)
| Release | Namespace | Chart |
|---------|-----------|-------|
| `keycloak` | `keycloak` | `codecentric/keycloakx` |
| `openfga` | `openfga` | `openfga/openfga` |
| `clickhouse` | `langfuse` | `bitnami/clickhouse` |
| `langfuse` | `langfuse` | `langfuse/langfuse` |

### Manifests Applied (`02-deploy-control-plane.sh`)
- `platform/control-plane-manifests/otel-collector.yaml` — OTel collector in `otel-system`
- `platform/control-plane-manifests/istio-ingress-gateway.yaml` — `platform-gateway` + HTTPRoutes
- `platform/control-plane-manifests/agentregistry.yaml` — AgentRegistry in `agentregistry`

### Keycloak Configuration (`03-configure-keycloak.sh`)
- Realm: `platform` (renamed from `agents` — control-plane auth for org/user management)
- Client scopes: `profile`, `email`, `organization`
- Clients: `agent-gateway`, `agentregistry`
- Organizations enabled on realm
- Initial org: `acme` (domain `acme.example.com`)
- K8s OIDC identity provider (for SA token client assertions)
- Initial Access Token for DCR stored in `platform-system/keycloak-initial-access-token`

### Ingress Routes
The `platform-gateway` LoadBalancer (internal NLB) routes:
| Path | Service |
|------|---------|
| `/auth/*` | `keycloak.keycloak:8080` |
| `/openfga/*` | `openfga.openfga:8080` |
| `/registry/*` | `agentregistry.agentregistry:8080` |
| `/langfuse/*` | `langfuse-web.langfuse:3000` |

---

## Accessing Services (Port-Forwards)

```bash
# Keycloak admin UI — http://localhost:18080
kubectl --context agentic-cp port-forward -n keycloak svc/keycloak 18080:8080

# Langfuse UI — http://localhost:13000
kubectl --context agentic-cp port-forward -n langfuse svc/langfuse-web 13000:3000

# AgentRegistry UI — http://localhost:18082/registry
kubectl --context agentic-cp port-forward -n agentregistry svc/agentregistry 18082:8080
```

---

## Cleanup

To tear down all Phase 3a resources:

```bash
# Delete Helm releases
cd platform/control-plane
helmfile --kube-context agentic-cp destroy

# Delete additional manifests
kubectl --context agentic-cp delete -f platform/control-plane-manifests/otel-collector.yaml
kubectl --context agentic-cp delete -f platform/control-plane-manifests/istio-ingress-gateway.yaml
kubectl --context agentic-cp delete -f platform/control-plane-manifests/agentregistry.yaml

# Delete namespaces (removes all resources within)
kubectl --context agentic-cp delete -f platform/control-plane-manifests/namespaces.yaml

# Delete AWS resources
aws rds delete-db-instance \
  --db-instance-identifier agentic-cp-postgres \
  --skip-final-snapshot --region us-east-1

aws elasticache delete-replication-group \
  --replication-group-id agentic-cp-redis --region us-east-1

aws s3 rb s3://agentic-platform-langfuse-cp --force --region us-east-1
```
