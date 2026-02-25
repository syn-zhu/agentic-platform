# Example 05: Atlas Performance Analyzer

An agent that analyzes MongoDB Atlas cluster performance — slow queries, index recommendations, alerts, and system health — using the official MongoDB MCP server deployed via the kmcp operator.

## What It Demonstrates

- **Third-party MCP server via kmcp** — the `MCPServer` CRD tells the kmcp operator to create a Deployment + Service running the official MongoDB MCP server. No manual Deployment YAML required.
- **HTTP transport mode** — the MongoDB MCP server runs in Streamable HTTP mode (`MDB_MCP_TRANSPORT=http`), avoiding the stdio adapter sidecar entirely.
- **Scoped tool access** — the agent only gets read-only tools. Write tools (`create-index`, `atlas-create-access-list`) are excluded from `toolNames` as defense-in-depth alongside `MDB_MCP_READ_ONLY=true`.
- **Split authentication** — Atlas API uses Service Account credentials (Secret); database queries use **AWS IAM via IRSA** (`MONGODB-AWS` auth mechanism) — no database password to manage.
- **Structured analysis procedures** — the system prompt contains 5 step-by-step procedures (health check, slow queries, index analysis, query optimization, resource monitoring) telling the agent exactly which tools to call and what to report.

## Architecture

```
                    example-atlas-analyzer namespace
┌────────────────────────────────────────────────────────────┐
│                                                            │
│  MCPServer CR ──(kmcp operator)──► Deployment + Service    │
│  "mongodb-mcp-server"              mongodb-mcp-server      │
│                                    ┌────────────────────┐  │
│                                    │ mongodb/mongodb-    │  │
│                                    │ mcp-server:latest   │  │
│                                    │                     │  │
│                                    │ HTTP transport :3000│  │
│                                    │ READ_ONLY = true    │  │
│                                    │                     │  │
│                                    │ Atlas API: Secret   │  │
│                                    │ DB auth:  IRSA/IAM  │  │
│                                    └─────────┬──────────┘  │
│                                              │ /mcp        │
│                                              │             │
│  Agent CR                                    │             │
│  "atlas-analyzer" ───────(MCP over HTTP)─────┘             │
│  ┌──────────────────────────────────────────────────┐      │
│  │  5 analysis procedures in system prompt          │      │
│  │                                                  │      │
│  │  Atlas tools:  atlas-list-clusters,              │      │
│  │    atlas-inspect-cluster,                        │      │
│  │    atlas-get-performance-advisor,                │      │
│  │    atlas-list-alerts, ...                        │      │
│  │                                                  │      │
│  │  DB tools:  find, aggregate, explain,            │      │
│  │    collection-indexes, db-stats, ...             │      │
│  └──────────────────────────────────────────────────┘      │
└────────────────────────────────────────────────────────────┘
         │                             │
         ▼ (A2A via waypoint)          ▼ (HTTPS + TLS)
   AgentGateway proxy           cloud.mongodb.com
                                + Atlas cluster endpoint
```

**How authentication works:**
- **Atlas API** (management tools like `atlas-list-clusters`): OAuth2 client credentials from the `atlas-credentials` Secret (`MDB_MCP_API_CLIENT_ID` + `MDB_MCP_API_CLIENT_SECRET`), injected via `envFrom`.
- **Database** (query tools like `find`, `explain`): The agent dynamically connects via `atlas-connect-cluster`, which uses `authMechanism=MONGODB-AWS`. The MongoDB driver automatically picks up AWS credentials from the IRSA-projected service account token — no database password to manage.

## Prerequisites

1. Platform deployed (`platform/manifests/`)
2. MongoDB Atlas account with:
   - An [API Service Account](https://www.mongodb.com/docs/atlas/api/service-accounts/) (Organization or Project level)
   - At least **Project Read Only** role (for cluster inspection and Performance Advisor)
   - A cluster to analyze
3. The EKS cluster's NAT gateway IP added to the Atlas project's [IP Access List](https://www.mongodb.com/docs/atlas/security/ip-access-list/) (required for database connections)

## Deploy

### Step 1: Create IAM Role for IRSA

Create an IAM role that the MongoDB MCP server pod will assume for database authentication:

```bash
# Get the OIDC provider URL for your EKS cluster
OIDC_PROVIDER=$(aws eks describe-cluster \
  --name agentic-platform \
  --query "cluster.identity.oidc.issuer" \
  --output text | sed 's|https://||')

ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)

# Create the trust policy
cat > /tmp/trust-policy.json <<EOF
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
          "${OIDC_PROVIDER}:sub": "system:serviceaccount:example-atlas-analyzer:mongodb-mcp-server",
          "${OIDC_PROVIDER}:aud": "sts.amazonaws.com"
        }
      }
    }
  ]
}
EOF

# Create the IAM role (no extra policies needed — Atlas handles authorization)
aws iam create-role \
  --role-name atlas-mcp-reader \
  --assume-role-policy-document file:///tmp/trust-policy.json
```

> **Note:** The IAM role doesn't need any AWS policies attached. MongoDB Atlas uses its own database user authorization to control what this role can access.

### Step 2: Create Atlas Database User (IAM type)

In the Atlas Console:
1. Go to **Database Access** → **Add New Database User**
2. Authentication Method: **AWS IAM** → **IAM Role**
3. AWS IAM Role ARN: `arn:aws:iam::<account-id>:role/atlas-mcp-reader`
4. Database User Privileges: **Read Any Database** (or scope to your specific database)

### Step 3: Create the Secret and Deploy

```bash
# Create namespace
kubectl create namespace example-atlas-analyzer

# Create the Atlas API credentials secret (database auth uses IRSA, not this secret)
kubectl create secret generic atlas-credentials \
  -n example-atlas-analyzer \
  --from-literal=MDB_MCP_API_CLIENT_ID="<your-client-id>" \
  --from-literal=MDB_MCP_API_CLIENT_SECRET="<your-client-secret>"

# Apply
kubectl apply -f examples/05-atlas-analyzer/manifests.yaml
```

### Step 4: Verify

```bash
kubectl get mcpserver -n example-atlas-analyzer
kubectl get pods -n example-atlas-analyzer -w
kubectl get agent -n example-atlas-analyzer
```

You should see two pods: one for the MongoDB MCP server (managed by kmcp) and one for the atlas-analyzer agent (managed by kagent).

## Demo

### Via kagent UI

1. **"Give me a health overview of my Atlas cluster"**
   → Agent lists clusters, inspects the target, checks alerts, reports on connections/storage/IOPS

2. **"Analyze slow queries on my cluster"**
   → Agent calls Performance Advisor, cross-references with existing indexes, reports slow query patterns with recommendations

3. **"Check the indexes on the users collection"**
   → Agent lists indexes, analyzes schema, identifies missing or redundant indexes

4. **"Are there any active alerts?"**
   → Agent calls `atlas-list-alerts`, summarizes active alerts with severity and recommended actions

5. **"Explain the performance of this query: db.orders.find({status: 'pending', createdAt: {$gt: ISODate('2024-01-01')}})"**
   → Agent runs `explain`, analyzes execution plan stages, recommends index improvements

### Via A2A (curl)

```bash
# Port-forward the AgentGateway proxy
kubectl port-forward -n agentgateway-system svc/agentgateway-proxy 15003:80

# Send A2A request
curl -s http://localhost:15003/a2a/example-atlas-analyzer/atlas-analyzer \
  -H 'Content-Type: application/json' \
  -d '{
    "jsonrpc": "2.0",
    "method": "message/send",
    "id": 1,
    "params": {
      "message": {
        "role": "user",
        "parts": [{"kind": "text", "text": "Give me a health overview of my cluster"}]
      }
    }
  }'
```

## Cleanup

```bash
kubectl delete -f examples/05-atlas-analyzer/manifests.yaml

# Optionally clean up the IAM role
aws iam delete-role --role-name atlas-mcp-reader
```
