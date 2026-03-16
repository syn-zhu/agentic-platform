# EverMemOS Kubernetes Deployment Architecture

## 1. Component Inventory and Decisions

| Component | Deploy New or Reuse? | K8s Resource Type | Why |
|-----------|---------------------|-------------------|-----|
| **EverMemOS app** | New | Deployment | Stateless FastAPI app, horizontally scalable |
| **MongoDB 7.0** | New (dedicated) | StatefulSet | No existing MongoDB in cluster |
| **Elasticsearch 8.11.0** | New | StatefulSet | No existing ES in cluster |
| **Milvus 2.5.2** | New | StatefulSet | No existing vector DB in cluster |
| **etcd (for Milvus)** | New | StatefulSet | Milvus metadata store |
| **MinIO (for Milvus)** | New | StatefulSet | Milvus object storage |
| **Redis 7.2** | New | Deployment | Cache layer, ephemeral |
| **Embedding model** | External API (DeepInfra) | N/A | No GPU nodes in cluster |
| **Reranker model** | External API (DeepInfra) | N/A | No GPU nodes in cluster |
| **LLM** | Existing (OpenRouter/agentgateway) | N/A | Already provisioned |

---

## 2. Namespace: `evermemos`

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: evermemos
  labels:
    platform.agentic.io/component: evermemos
    istio.io/dataplane-mode: ambient
```

---

## 3. Architecture Diagram

```
                    +-----------------+
                    |  kagent agents  |
                    |  (any namespace)|
                    +--------+--------+
                             |
                    HTTP :1995 (via kube DNS)
                             |
                    +--------v--------+
                    |   evermemos     |  Namespace: evermemos
                    |   (Deployment)  |  Port 1995
                    +--+-+-+--+------+
                       | | |  |
          +------------+ | |  +-------------+
          |              | |                |
+---------v--+  +-------v-v---+  +---------v-------+
| MongoDB    |  | Elasticsearch|  | Milvus          |
| :27017     |  | :9200        |  | :19530          |
| (SS, 1 rep)|  | (SS, 1 rep)  |  | (SS, 1 rep)     |
+------------+  +--------------+  |  +etcd :2479    |
                                  |  +minio :9000   |
          +--------+              +-----------------+
          |        |
+---------v--+
| Redis      |
| :6379      |
| (Deploy)   |
+------------+
```

---

## 4. Resource Summary (Minimum Viable Deployment)

| Component | CPU Req | CPU Lim | Mem Req | Mem Lim | PVC |
|-----------|---------|---------|---------|---------|-----|
| EverMemOS app | 250m | 1 | 512Mi | 1Gi | - |
| MongoDB | 250m | 1 | 512Mi | 2Gi | 20Gi gp3 |
| Elasticsearch | 500m | 2 | 2Gi | 3Gi | 30Gi gp3 |
| Milvus | 500m | 2 | 2Gi | 4Gi | 20Gi gp3 |
| Milvus etcd | 100m | 0.5 | 256Mi | 512Mi | 5Gi gp3 |
| Milvus MinIO | 100m | 0.5 | 256Mi | 1Gi | 20Gi gp3 |
| Redis | 100m | 0.25 | 128Mi | 256Mi | - |
| **Total** | **1.8** | **7.25** | **5.7Gi** | **11.8Gi** | **95Gi** |

---

## 5. ConfigMap

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: evermemos-config
  namespace: evermemos
data:
  MEMSYS_HOST: "0.0.0.0"
  MEMSYS_PORT: "1995"
  LOG_LEVEL: "INFO"
  ENV: "production"
  MEMORY_LANGUAGE: "en"
  MONGODB_HOST: "evermemos-mongodb"
  MONGODB_PORT: "27017"
  MONGODB_DATABASE: "memsys"
  ES_HOSTS: "http://evermemos-elasticsearch:9200"
  SELF_ES_INDEX_NS: "memsys"
  MILVUS_HOST: "evermemos-milvus"
  MILVUS_PORT: "19530"
  SELF_MILVUS_COLLECTION_NS: "memsys"
  REDIS_HOST: "evermemos-redis"
  REDIS_PORT: "6379"
  REDIS_DB: "8"
  LLM_PROVIDER: "openai"
  LLM_MODEL: "x-ai/grok-4-fast"
  LLM_BASE_URL: "https://openrouter.ai/api/v1"
  LLM_TEMPERATURE: "0.3"
  LLM_MAX_TOKENS: "32768"
  VECTORIZE_PROVIDER: "deepinfra"
  VECTORIZE_BASE_URL: "https://api.deepinfra.com/v1/openai"
  VECTORIZE_MODEL: "Qwen/Qwen3-Embedding-4B"
  VECTORIZE_DIMENSIONS: "1024"
  RERANK_PROVIDER: "deepinfra"
  RERANK_BASE_URL: "https://api.deepinfra.com/v1/inference"
  RERANK_MODEL: "Qwen/Qwen3-Reranker-4B"
```

---

## 6. Secrets

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: evermemos-secrets
  namespace: evermemos
type: Opaque
stringData:
  MONGODB_USERNAME: "admin"
  MONGODB_PASSWORD: "<MONGODB_PASSWORD>"
  LLM_API_KEY: "<OPENROUTER_API_KEY>"
  VECTORIZE_API_KEY: "<DEEPINFRA_API_KEY>"
  RERANK_API_KEY: "<DEEPINFRA_API_KEY>"
```

---

## 7. Key Design Decisions

- **Raw YAML manifests** (consistent with rest of platform, no Helm)
- **External DeepInfra APIs** for embedding/reranking (no GPU nodes needed)
- **Single `evermemos` namespace** with Istio ambient mesh
- **ClusterIP services only** -- agents access via kube DNS
- **gp3 EBS volumes** for all persistent storage
- **Redis as ephemeral Deployment** (cache, not critical data)

---

## 8. Image Build

```bash
cd ~/EverMemOS
docker build --platform linux/amd64 \
  -t <ECR_REGISTRY>/agentic-platform/evermemos:v1.0.0 .
docker push <ECR_REGISTRY>/agentic-platform/evermemos:v1.0.0
```

---

## 9. Production Hardening (Future)

- MongoDB -> Amazon DocumentDB
- Elasticsearch -> Amazon OpenSearch Service
- Milvus -> Zilliz Cloud (managed Milvus)
- Redis -> Amazon ElastiCache
- EverMemOS app -> 2+ replicas
- CronJob backups (mongodump, ES snapshots to S3)
- ResourceQuota and LimitRange on namespace
