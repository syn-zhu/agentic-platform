# OpenFGA Integration Plan: Org-Scoped Authorization for Agentic Platform

## Overview

This plan adds **OpenFGA** to the existing Keycloak + AgentGateway platform to enable **org-scoped relationship-based access control (ReBAC)**. It replaces the inline CEL `matchExpressions` role checks in AgentgatewayPolicy with external authorization calls to OpenFGA via `openfga-envoy`.

**Key design principle: Platform provides plumbing, tenants/examples provide policy.**

The platform deploys OpenFGA, openfga-envoy, and the ext_authz wiring. Each tenant (or example) creates their own OpenFGA **store** with their own **authorization model** and **relationship tuples**. The platform has no opinion on role names, role hierarchies, or permission structures -- those are tenant-defined.

**What changes:**
- Tool-level authorization moves from CEL role expressions to OpenFGA relationship checks via `ext_authz`
- Roles become org-scoped (user has role X on tool_server Y in org Z) instead of globally flat (user has client role X)
- Authorization (who can do what) is fully managed in OpenFGA; Keycloak handles identity only
- Client roles in Keycloak are no longer used for authorization decisions

**What stays the same:**
- Keycloak remains the IDP (authentication, token issuance, token exchange)
- JWT validation at ingress and waypoint (issuer, audience, tenant claim)
- Per-audience STS token exchange (the lazy exchange + per-tool audience work we just landed)
- K8s SA federation for agent client authentication
- Dynamic Client Registration for tenant onboarding
- SPIFFE identity for agent-to-MCP-server identity (mesh mTLS)
- The three-layer architecture: ingress auth -> agent access -> tool access

**Reference:** This plan builds on the existing [PLAN.md](./PLAN.md). Phases 0 (Keycloak), 1 (namespace setup), 2.1 (MCP server), 2.2 (AgentgatewayBackend + HTTPRoute), and 3 (Agent CR + token exchange) remain largely unchanged. The primary changes are in Phase 0 (add OpenFGA platform components) and Phase 2.3 (replace CEL tool policy with ext_authz). The authorization model and tuples are fully example-scoped.

---

## Platform vs. Example Boundary

This is the most important distinction in the plan. Getting it wrong means example-specific concepts leak into the platform.

### Platform provides (shared infrastructure)

- **OpenFGA server** -- multi-tenant store backend (one store per tenant/example)
- **openfga-envoy** -- ext_authz gRPC adapter that translates waypoint check requests into OpenFGA Check calls
- **Network connectivity** -- egress from tenant namespaces to the `openfga` namespace
- **Store provisioning API** -- a script or controller that creates an OpenFGA store for a tenant and returns the `store_id`
- **Generic ext_authz contract** -- the waypoint forwards `jwt.sub`, `mcp.tool.name`, `jwt.claims.tenant` as metadata to openfga-envoy; openfga-envoy knows how to map these to an OpenFGA Check request

The platform does **not** define:
- Role names (no `analyst`, `operator`, `admin` at the platform level)
- Authorization models (no DSL in platform scripts)
- Relationship tuples
- Which roles can call which tools

### Example/tenant provides (per-tenant policy)

- **Authorization model (DSL)** -- defines types, relations, role vocabulary. Written to the tenant's own OpenFGA store during tenant setup.
- **Relationship tuples** -- org membership, role assignments, tool hierarchy. Written during tenant setup and managed via OpenFGA API at runtime.
- **AgentgatewayPolicy ext_authz configuration** -- points at openfga-envoy; tenant resolution is automatic from `jwt.claims.tenant`

Each example/tenant is free to define completely different authorization models. One tenant could use role-based inheritance (`analyst` < `operator` < `admin`), another could use flat direct permission grants, and a third could model project-level scoping within orgs. The platform doesn't care.

---

## Architecture

```
User (JWT) --> Ingress Gateway --> Agent --> [mesh mTLS] --> Waypoint --> MCP Server
                    |                |                          |
                    |                |                          |-- JWT validation (exchanged token)
                    |                |                          |-- ext_authz (gRPC) --> openfga-envoy --> OpenFGA
                    |                |                          |     check: user=jwt.sub, relation=can_call,
                    |                |                          |            object=tool:<mcp-server>/<tool-name>
                    |                |                          |     (store name from jwt.claims.tenant -> store_id via ListStores)
                    |                |                          |
                    |                |                          |-- source.identity (SPIFFE, still in CEL
                    |                |                          |     for agent identity check)
                    |                |
                    |                |-- STS exchange (Keycloak, per-audience)
                    |                |
                    |                |-- Agent access policy:
                    |                      jwt.tenant (unchanged, CEL)
                    |
                    |-- Ingress auth policy:
                          valid JWT + has(jwt.claims.tenant) (unchanged)
```

### Component Roles

| Component | Role | Scope | Protocol |
|-----------|------|-------|----------|
| **Keycloak** | IDP: authentication, token issuance, token exchange | Platform | OIDC, RFC 8693 |
| **OpenFGA** | Authorization store: relationship tuples + model evaluation | Platform (infra), tenant (data) | gRPC / HTTP API |
| **openfga-envoy** | Policy decision point: ext_authz gRPC -> OpenFGA Check (shared, per-request tenant resolution) | Platform | Envoy ext_authz v3 gRPC |
| **AgentGateway waypoint** | Policy enforcement point: calls openfga-envoy via extAuth | Per-tenant | gRPC ext_authz |
| **Authorization model + tuples** | Policy definition: what roles exist, what they can access | Per-tenant | OpenFGA DSL + Write API |

---

## Phase 0: Platform Prerequisites

### 0.1 Deploy OpenFGA (platform-level)

Deploy OpenFGA as a platform service in a dedicated namespace. This is shared infrastructure -- all tenants use the same OpenFGA server, but each gets their own store.

**Namespace:** `openfga`

**Components:**
- OpenFGA server (Deployment + Service) -- multi-tenant store backend
- PostgreSQL backend (or reuse existing RDS with a separate database)
- openfga-envoy (Deployment + Service) -- ext_authz gRPC adapter

**Helm chart:** OpenFGA publishes an [official Helm chart](https://github.com/openfga/helm-charts). Deploy with:
```yaml
# platform/values/openfga.yaml
openfga:
  datastore:
    engine: postgres
    uri: <RDS connection string for openfga database>
  grpc:
    enabled: true
  http:
    enabled: true
```

**openfga-envoy:** A **single shared** openfga-envoy instance deployed in the `openfga` namespace. Connects to the OpenFGA server via gRPC and exposes the Envoy ext_authz gRPC API on **port 9002**.

**Vendored and patched:** The upstream `openfga-envoy` only supports per-instance `store_id` (one store per deployment). We vendor the repo at `vendor/openfga-envoy/` and patch it for **per-request store name resolution**:

- The waypoint sets `x-fga-store-name` from `jwt.claims.tenant` via `requestMetadata`.
- openfga-envoy reads the `x-fga-store-name` header, resolves the store name to a `store_id` via the OpenFGA `ListStores` API (cached in a `sync.Map`).
- A per-store client cache (`sync.Map` of `store_id -> *client.OpenFgaClient`) ensures thread-safe concurrent requests to different stores.
- If `x-fga-store-id` header is present, it takes precedence over store name resolution (direct store_id override).
- Falls back to the config-level `store_id` if neither header is present.

**Key source files (patched):**
- `vendor/openfga-envoy/extauthz/internal/server/authz/authz.go` -- `resolveClient()`, `resolveStoreIDByName()`, `getOrCreateClient()`, store name/client caches
- `vendor/openfga-envoy/extauthz/internal/server/config/config.go` -- `StoreIDHeader`, `StoreNameHeader` fields on `Server` struct
- `vendor/openfga-envoy/extauthz/cmd/extauthz/main.go` -- passes new config fields to `authz.Config`
- `vendor/openfga-envoy/extauthz/Dockerfile` -- multi-stage build (golang:1.22-alpine)

**Build instructions:**
```bash
cd vendor/openfga-envoy/extauthz
docker build -t <ECR_REPO>/openfga-envoy:latest .
docker push <ECR_REPO>/openfga-envoy:latest
# Then update the image in platform/manifests/openfga.yaml
```

**Additional details:**
- **Port is 9002** (not 9191 as originally assumed)
- **Config is file-based** (`--config config.yaml`), env prefix `OPENFGA_EXTAUTHZ` can override fields.
- **Extractors** read `user`, `object`, `relation` from HTTP headers in the CheckRequest (via `header` extractor type). The waypoint's `requestMetadata` must set these headers.
- **No published container images upstream.** Must build from the vendored source.
- **Modes:** ENFORCE (deny on failure), MONITOR (log-only, always allow), DISABLED

See `platform/manifests/openfga.yaml` for the actual deployment manifests (ConfigMap + Deployment + Service).

**Network policy:** Tenant namespaces need egress to `openfga` namespace (the waypoint calls openfga-envoy). Add to tenant NetworkPolicy:
```yaml
# Egress to OpenFGA (ext_authz)
- to:
    - namespaceSelector:
        matchLabels:
          kubernetes.io/metadata.name: openfga
```

### 0.2 Keycloak changes (minimal)

Keycloak's role in authorization is **reduced**. With OpenFGA handling authorization:

- **Client roles are no longer used for authorization decisions.** The CEL expressions that read `jwt.resource_access[...].roles` are replaced by ext_authz -> OpenFGA checks. Client roles can still exist in Keycloak for backward compatibility, but the waypoint no longer evaluates them.
- **The keycloak-openfga-event-publisher is not needed.** Since we're managing authorization directly in OpenFGA (not syncing from Keycloak), there's no event publisher to install. Keycloak handles identity (who are you?), OpenFGA handles authorization (what can you do?).
- **Group membership (`/tenants/<name>`) stays in Keycloak** for the `tenant` claim in JWT tokens. The `jwt.claims.tenant` check at the agent access policy layer is unchanged.

No Keycloak Helm values changes required for OpenFGA integration.

### 0.3 Platform setup script: `scripts/07-configure-openfga.sh`

A platform-level script that initializes the OpenFGA server. Does **not** create any stores or models -- that's tenant-scoped.

```bash
#!/usr/bin/env bash
# Platform-level OpenFGA initialization
# Creates the openfga namespace, applies manifests, and verifies the server is healthy.

# 1. Apply OpenFGA manifests
kubectl apply -f platform/manifests/openfga.yaml

# 2. Wait for OpenFGA server to be ready
kubectl rollout status deployment/openfga -n openfga --timeout=120s
kubectl rollout status deployment/openfga-envoy -n openfga --timeout=120s

# 3. Verify health
OPENFGA_URL="http://openfga.openfga.svc.cluster.local:8080"
kubectl run --rm -i --restart=Never openfga-health-check \
  --image=curlimages/curl -- curl -sf "$OPENFGA_URL/healthz"

echo "OpenFGA platform infrastructure is ready."
echo "Tenants can now create stores via: POST $OPENFGA_URL/stores"
```

That's it for the platform. No stores, no models, no tuples.

---

## Example 07: Per-Tenant Authorization with OpenFGA

Everything below is **example-scoped**. It lives in `examples/07-tool-access-policy/`, not in `platform/`.

### Tenant store and model setup (`setup-openfga.sh`)

Each tenant (or in this case, the example) creates its own OpenFGA store and writes its own authorization model. This script runs as part of example setup, after the platform is deployed.

```bash
#!/usr/bin/env bash
# Example 07: Create OpenFGA store and authorization model for the example tenant.
# This is example-specific -- other examples/tenants define their own models.
#
# IMPORTANT: The store name MUST match the tenant name (jwt.claims.tenant).
# openfga-envoy resolves x-fga-store-name -> store_id via ListStores, matching by name.

OPENFGA_URL="http://openfga.openfga.svc.cluster.local:8080"
TENANT_NAME="acme"  # Must match jwt.claims.tenant for this tenant

# 1. Create a store for this tenant (name = tenant name for automatic resolution)
STORE_RESPONSE=$(curl -sf -X POST "$OPENFGA_URL/stores" \
  -H "Content-Type: application/json" \
  -d "{\"name\": \"$TENANT_NAME\"}")
STORE_ID=$(echo "$STORE_RESPONSE" | jq -r '.id')

# 2. Write the authorization model (example-specific role vocabulary)
MODEL_RESPONSE=$(curl -sf -X POST "$OPENFGA_URL/stores/$STORE_ID/authorization-models" \
  -H "Content-Type: application/json" \
  -d @openfga-model.json)
MODEL_ID=$(echo "$MODEL_RESPONSE" | jq -r '.authorization_model_id')

echo "OpenFGA store created: $STORE_ID (name=$TENANT_NAME)"
echo "Authorization model: $MODEL_ID"
echo "openfga-envoy will auto-resolve tenant '$TENANT_NAME' -> store '$STORE_ID'"
```

### Authorization model (`openfga-model.json`)

This file lives in `examples/07-tool-access-policy/` and defines the example's role vocabulary and permission structure. Other examples would have completely different models.

```dsl
model
  schema 1.1

type user

type organization
  relations
    define member: [user]

type tool_server
  relations
    define org: [organization]
    define member_of_org: member from org
    # Example-specific roles -- other tenants define their own
    define analyst: [user] and member_of_org
    define operator: [user] and member_of_org
    define admin: [user] and member_of_org

type tool
  relations
    define parent: [tool_server]
    define can_call: direct_grant or analyst_on_parent or operator_on_parent or admin_on_parent
    define direct_grant: [user]
    define analyst_on_parent: analyst from parent
    define operator_on_parent: operator from parent
    define admin_on_parent: admin from parent
```

**This is entirely example-specific.** The role names (`analyst`, `operator`, `admin`), the inheritance structure (`can_call` via role lookup on parent), and the type hierarchy are all defined by this example. A different tenant could define roles like `viewer`, `editor`, `approver` with completely different semantics.

**Per-tool restriction:** The model above gives all roles `can_call` on all tools of a tool_server via inheritance. To restrict specific tools (e.g., `execute_query` to operator+admin only, `modify_config` to admin only), use the `direct_grant` relation -- only write `can_call` tuples for the users/roles that should have access to that tool, instead of relying on inheritance. See the tuple examples below.

### Relationship tuples (in `setup-tenant.sh`)

The tenant setup script gains OpenFGA steps. These are written during tenant onboarding and managed via OpenFGA API at runtime.

```bash
# --- OpenFGA tuple writes (after Keycloak user/DCR setup) ---

# Retrieve the store_id (created in setup-openfga.sh with name matching the tenant)
OPENFGA_URL="http://openfga.openfga.svc.cluster.local:8080"
STORE_ID=$(curl -sf "$OPENFGA_URL/stores" | jq -r '.stores[] | select(.name=="acme") | .id')

# Org membership
fga tuple write user:alice member organization:acme --store-id $STORE_ID
fga tuple write user:bob   member organization:acme --store-id $STORE_ID

# Tool server belongs to org
fga tuple write organization:acme org tool_server:policy-mcp-server --store-id $STORE_ID

# Role assignments (example-specific roles)
fga tuple write user:alice analyst tool_server:policy-mcp-server --store-id $STORE_ID
fga tuple write user:bob   admin   tool_server:policy-mcp-server --store-id $STORE_ID

# Tool hierarchy (tool -> parent -> tool_server)
fga tuple write tool_server:policy-mcp-server parent tool:policy-mcp-server/list_reports  --store-id $STORE_ID
fga tuple write tool_server:policy-mcp-server parent tool:policy-mcp-server/read_report   --store-id $STORE_ID
fga tuple write tool_server:policy-mcp-server parent tool:policy-mcp-server/execute_query  --store-id $STORE_ID
fga tuple write tool_server:policy-mcp-server parent tool:policy-mcp-server/modify_config  --store-id $STORE_ID

# Per-tool restriction: execute_query is operator/admin only, modify_config is admin only.
# Since the model uses role inheritance via can_call, all roles get can_call on all tools
# by default. To restrict, we can either:
#   (a) Use a different model with explicit grants (no role inheritance on tools), or
#   (b) Use conditional tuples / contextual tuples at check time.
#
# For this example, we use approach (a) for modify_config and execute_query:
# Instead of relying on role inheritance, we write direct_grant tuples for restricted tools.
# This means we DON'T use the role-based can_call for these tools -- we override with direct grants.
#
# See the authorization model and Open Questions for the design tradeoff.

# Verify permissions
fga query check user:alice can_call tool:policy-mcp-server/list_reports   --store-id $STORE_ID  # -> allowed (analyst)
fga query check user:alice can_call tool:policy-mcp-server/execute_query  --store-id $STORE_ID  # -> allowed (analyst inherits can_call)
fga query check user:bob   can_call tool:policy-mcp-server/modify_config  --store-id $STORE_ID  # -> allowed (admin)
```

**Role promotion (demo):**
```bash
# Promote alice from analyst to operator
fga tuple delete user:alice analyst tool_server:policy-mcp-server --store-id $STORE_ID
fga tuple write  user:alice operator tool_server:policy-mcp-server --store-id $STORE_ID

# Now alice has operator-level access
fga query check user:alice can_call tool:policy-mcp-server/execute_query  --store-id $STORE_ID  # -> allowed
```

### Check flow at request time

When the waypoint intercepts an MCP tool call, it sends an ext_authz gRPC check to openfga-envoy with metadata extracted from the validated JWT and MCP context:

```
user:      jwt.sub (from the exchanged token)
object:    tool:<mcp-server>/<tool-name> (from mcp.tool.name)
relation:  can_call
store:     jwt.claims.tenant (resolved to store_id by openfga-envoy via cached ListStores)
```

openfga-envoy translates this into an OpenFGA `Check` call:
```json
{
  "user": "user:alice",
  "relation": "can_call",
  "object": "tool:policy-mcp-server/execute_query"
}
```

OpenFGA evaluates the relationship graph in the tenant's store and returns allow/deny.

---

## Phase 2.3 (revised): Tool Access Policy with ext_authz

Replace the current CEL `matchExpressions` with an `extAuth` policy that delegates to openfga-envoy.

### AgentGateway policy attachment constraints

The AgentgatewayPolicy API has strict rules about which policy sections can target which resources:

- **`traffic`** (including `extAuth`, `jwtAuthentication`, `authorization`) can only target `Gateway`, `HTTPRoute`, `GRPCRoute`, or `ListenerSet`
- **`backend`** (including `backend.mcp.authorization`, `backend.mcp.authentication`) can target all of the above plus `AgentgatewayBackend` and `Service`

This means `traffic.extAuth` **cannot** be placed on a policy that targets an `AgentgatewayBackend`. We need two separate policies:

1. **Backend policy** (targets `AgentgatewayBackend`) -- JWT authentication + CEL authorization (agent identity check)
2. **Traffic policy** (targets the `HTTPRoute`) -- ext_authz to openfga-envoy

### Before (CEL inline, single policy):
```yaml
authorization:
  policy:
    matchExpressions:
      - 'mcp.tool.name in ["list_reports", "read_report"] && ("analyst" in jwt.resource_access[...].roles || ...)'
      - 'mcp.tool.name == "execute_query" && ("operator" in jwt.resource_access[...].roles || ...)'
      - 'mcp.tool.name == "modify_config" && "admin" in jwt.resource_access[...].roles && source.identity.serviceAccount == "tool-policy-agent"'
```

### After (two policies: backend MCP + traffic ext_authz):

**Policy 1: MCP backend policy** (targets AgentgatewayBackend)

Handles JWT validation and agent identity check. This is unchanged from the original plan except that the role-based CEL expressions are removed -- OpenFGA handles role checks now.

```yaml
apiVersion: agentgateway.dev/v1alpha1
kind: AgentgatewayPolicy
metadata:
  name: tool-mcp-policy
  namespace: example-tool-policy
spec:
  targetRefs:
    - group: agentgateway.dev
      kind: AgentgatewayBackend
      name: policy-mcp
  backend:
    mcp:
      authentication:
        provider: Keycloak
        issuer: "http://keycloak.keycloak.svc.cluster.local:8080/realms/agents"
        audiences:
          - "system:serviceaccount:example-tool-policy:policy-mcp-server"
        jwks:
          jwksPath: "/realms/agents/protocol/openid-connect/certs"
          backendRef:
            group: ""
            kind: Service
            name: keycloak
            namespace: keycloak
            port: 8080
      authorization:
        policy:
          matchExpressions:
            # Agent identity check stays in CEL (SPIFFE, not a relationship).
            # This is infrastructure-level -- which workload is making the call.
            - 'mcp.tool.name != "modify_config" || source.identity.serviceAccount == "tool-policy-agent"'
```

**Policy 2: ext_authz traffic policy** (targets HTTPRoute)

Handles the OpenFGA authorization check. This policy targets the HTTPRoute that routes to the MCP backend, so it fires on all traffic to the MCP server.

```yaml
apiVersion: agentgateway.dev/v1alpha1
kind: AgentgatewayPolicy
metadata:
  name: tool-extauth-policy
  namespace: example-tool-policy
spec:
  targetRefs:
    - group: gateway.networking.k8s.io
      kind: HTTPRoute
      name: policy-mcp
  traffic:
    extAuth:
      backendRef:
        group: ""
        kind: Service
        name: openfga-envoy
        namespace: openfga
        port: 9002
      grpc:
        requestMetadata:
          x-fga-user: 'jwt.sub'
          x-fga-object: '"tool:" + mcp.tool.name'
          x-fga-relation: '"can_call"'
          x-fga-store-name: 'jwt.claims.tenant'  # Resolved to store_id by openfga-envoy via ListStores cache
```

### Limitation: ext_authz at traffic level vs. MCP backend level

Because `extAuth` is a traffic-level policy, it operates at the HTTP layer, not the MCP layer. This has two consequences:

1. **`call_tool` works fine.** The ext_authz check fires before the request reaches the MCP handler. If OpenFGA denies, the request gets a 403. If it allows, the MCP handler processes the call.

2. **`list_tools` filtering does not work.** The MCP `backend.mcp.authorization` CEL has a unique property: it can evaluate per-tool during `list_tools` and filter individual tools from the response (tools the user can't access are omitted from the list). The traffic-level ext_authz fires once for the whole `list_tools` request -- it can't filter individual tools because it doesn't know which tools will be in the response. This means all tools will be visible in `list_tools`, but unauthorized `call_tool` requests will be denied.

**Ideal solution (upstream enhancement):** Add `ExtAuth` to the `BackendMCP` type in AgentGateway so that ext_authz runs inside the MCP handler with full per-tool context. This would enable both `call_tool` deny/allow and `list_tools` per-tool filtering via OpenFGA. This is worth raising as a feature request with the AgentGateway community.

**Workaround for `list_tools` filtering:** Keep a minimal CEL expression in `backend.mcp.authorization` that calls out to a helper (or duplicates the role check logic) to filter `list_tools`. Alternatively, accept that `list_tools` shows all tools and rely on `call_tool` denial as the enforcement point. For many use cases this is acceptable -- the user sees the tool exists but gets a clear error if they try to call it without permission.

### Key design decisions:

1. **JWT authentication stays in the backend policy.** The MCP handler validates the exchanged token (issuer, audience, signature). OpenFGA only handles authorization (does this user have permission?), not authentication (is this token valid?).

2. **Agent identity check stays in CEL on the backend policy.** The `source.identity.serviceAccount == "tool-policy-agent"` check is infrastructure-level (SPIFFE identity from mesh mTLS). It doesn't make sense to model this in OpenFGA.

3. **ext_authz is a separate traffic policy on the HTTPRoute.** This is required by the AgentGateway API -- `traffic.extAuth` cannot target an `AgentgatewayBackend`.

4. **Store resolution is automatic.** The waypoint forwards `jwt.claims.tenant` as `x-fga-store-name` in `requestMetadata`. The shared openfga-envoy instance resolves the store name to a `store_id` via cached `ListStores`. No per-tenant configuration needed in the traffic policy.

5. **Two policies coexist.** Both policies apply to traffic reaching the MCP server: the traffic policy (ext_authz) fires first at the HTTP level, then the backend policy (JWT + CEL) fires at the MCP level. Both must pass for the request to succeed.

### Agent access policy (Layer 2) -- unchanged

The agent access policy (tenant check) stays as CEL:
```yaml
authorization:
  policy:
    matchExpressions:
      - 'jwt.claims.tenant == "acme"'
```

This is a simple claim check, not a relationship query. Moving it to OpenFGA would add latency for no benefit.

---

## Implementation Order

1. **Phase 0.1** -- Deploy OpenFGA server + openfga-envoy to `openfga` namespace (platform manifests + Helm values)
2. **Phase 0.3** -- Platform setup script (`scripts/07-configure-openfga.sh`) -- just deploys and health-checks
3. **Example setup** -- `setup-openfga.sh`: create store, write authorization model, write tuples
4. **Phase 2.3** -- Replace CEL tool policy with ext_authz -> openfga-envoy in example manifests
5. **Phase 1.2** -- Update `setup-tenant.sh` to include OpenFGA tuple writes alongside DCR
6. **Phase 4** -- Update README + walkthrough

Phases 0 (Keycloak upgrade), 1.1 (namespace), 2.1 (MCP server), 2.2 (AgentgatewayBackend), 2.4 (agent access policy), and 3 (Agent CR) from the original plan are unchanged.

**Note:** Phase 0.2 (keycloak-openfga-event-publisher) from the previous revision is removed. We don't need to sync anything from Keycloak to OpenFGA. Keycloak handles identity; OpenFGA handles authorization. They don't need to talk to each other.

---

## What This Enables (vs. current CEL approach)

| Capability | CEL matchExpressions (current) | OpenFGA (proposed) |
|---|---|---|
| **Org-scoped roles** | No -- client roles are global within the Keycloak client | Yes -- roles are scoped to (user, tool_server, org) tuples |
| **Per-tenant models** | No -- CEL expressions are the same structure everywhere | Yes -- each tenant defines their own authorization model DSL |
| **Custom role vocabulary** | Limited -- roles must exist as Keycloak client roles | Unlimited -- tenants define whatever relations they want |
| **Role hierarchy** | Manual CEL duplication (`admin` implies `operator` implies `analyst`) | Modeled via OpenFGA relation inheritance (tenant-defined) |
| **Dynamic permissions** | Requires redeploying AgentgatewayPolicy YAML | Write/delete tuples via API (no redeploy) |
| **Audit trail** | None -- CEL is stateless | OpenFGA stores all tuples; can query "who has access to what?" |
| **Cross-org isolation** | Tenant claim check only | Each tenant has its own store; roles are scoped to org |
| **Delegated admin** | FGAP V2 in Keycloak (preview) | Tenant admins write tuples via OpenFGA API (or via admin service) |
| **Policy-as-data** | Policy is embedded in YAML | Model is per-tenant; tuples are per-tenant. No platform-level policy. |

---

## Open Questions / Risks

### 1. openfga-envoy maturity and per-request multi-tenancy (RESOLVED)

openfga-envoy upstream is **WIP** with no published container images. The upstream code only supports per-instance `store_id`, meaning one deployment per tenant.

**Resolution:** We vendor the repo at `vendor/openfga-envoy/` and patch it for per-request store name resolution:
- New `store_name_header` config field: reads store name from `x-fga-store-name` request header
- Resolves store name to `store_id` via cached `ListStores` API call
- Per-store client cache (`sync.Map`) for thread-safe concurrent multi-store requests
- Optional `store_id_header` for direct `store_id` override via `x-fga-store-id` header
- Multi-stage Dockerfile for building from source

This gives us a **single shared openfga-envoy instance** serving all stores, with the waypoint injecting `x-fga-store-name` from `jwt.claims.tenant` into `requestMetadata`. No per-store deployments needed. This change is generic and could be contributed upstream.

### 2. Authorization model design (per-tenant decision)

The right model is a per-tenant decision, not a platform one. But for the example, key questions remain:
- **Role-based inheritance vs. direct permission grants:** Should `analyst` on a tool_server automatically grant `can_call` on all tools of that server? Or should each tool have explicit permission tuples?
- **Tool registration:** How are `tool -> parent -> tool_server` tuples created? Manually in setup scripts? Automatically via a controller watching MCPServer CRs?

**Recommendation for the example:** Start with role-based inheritance (simpler, fewer tuples). Document the direct-grant alternative for tenants that want finer control.

### 3. Latency

Every MCP tool call now requires an ext_authz gRPC round-trip to openfga-envoy, which makes an OpenFGA Check call. This adds latency to the hot path.

**Mitigation:**
- Deploy openfga-envoy close to the waypoint (same node pool)
- OpenFGA supports caching at the Check level
- openfga-envoy can be deployed as a sidecar on the waypoint pod for local gRPC (no network hop)
- For high-throughput scenarios, consider caching check results at the waypoint level (TTL-based)

### 4. ext_authz cannot target AgentgatewayBackend (confirmed)

The AgentGateway API validation rules confirm that `traffic.extAuth` can only target `Gateway`, `HTTPRoute`, `GRPCRoute`, or `ListenerSet` -- **not** `AgentgatewayBackend`. This means ext_authz and `backend.mcp` policies cannot be in the same policy resource when targeting an AgentgatewayBackend.

**Resolution:** Use two separate policies (as documented in Phase 2.3 above). The backend policy targets the AgentgatewayBackend (JWT auth + CEL agent identity), and the traffic policy targets the HTTPRoute (ext_authz to OpenFGA).

**Consequence:** `list_tools` per-tool filtering via OpenFGA is not possible with the current API. The ext_authz fires once for the whole HTTP request, not per-tool. Enforcement happens at `call_tool` time.

**Upstream enhancement:** Adding `ExtAuth` to `BackendMCP` would allow the MCP handler to call OpenFGA per-tool during both `list_tools` and `call_tool`. This is worth proposing to the AgentGateway community.

### 5. Token claims for OpenFGA

The ext_authz check needs `jwt.sub` to identify the user. The exchanged token's `sub` claim must match the user identifier used in OpenFGA tuples (e.g., `user:<keycloak-user-id>`). Verify that Keycloak's token exchange preserves the original user's `sub` claim (it should, since we're doing audience scoping, not impersonation).

### 6. Per-tenant store provisioning

The current plan uses a manual script (`setup-openfga.sh`) to create stores. For a production multi-tenant platform, this should be automated -- e.g., a controller that watches for new tenant namespaces and creates an OpenFGA store, or a step in the tenant onboarding API.

**Important:** The store name MUST match `jwt.claims.tenant` for automatic resolution by openfga-envoy. The `resolveStoreID()` function iterates `ListStores` results and matches by `store.GetName() == tenant`.

### 7. Tenant isolation for direct OpenFGA API access

OpenFGA stores are purely logical partitions -- all stores share the same PostgreSQL tables, tagged by `store_id`. OpenFGA has **no built-in per-store access control**. Any client that can reach the OpenFGA API can read/write any store.

**Production isolation path:**
1. Put OpenFGA behind the service mesh (it's already in the `openfga` namespace with ambient mode).
2. Expose the OpenFGA API through a waypoint with a policy that maps `jwt.claims.tenant` to the allowed `store_id` in the URL path (`/stores/{store_id}/...`).
3. Tenant admins authenticate via JWT and are scoped to their store by path-based access policy.
4. Network policy prevents direct pod-to-pod access to OpenFGA from tenant namespaces -- all access goes through the waypoint.

This makes per-tenant stores the natural choice: the URL path `/stores/{store_id}` becomes the isolation boundary enforced by the mesh.

**For the example:** Direct OpenFGA API access without mesh isolation is acceptable (single-tenant demo). Production isolation is a follow-up.

### 8. Self-serve tenant administration

Tenant admins can manage their own authorization model and tuples via the **OpenFGA REST API** directly. OpenFGA has a full CRUD API:
- `POST /stores/{store_id}/authorization-models` -- create/update model
- `POST /stores/{store_id}/write` -- add/remove tuples
- `POST /stores/{store_id}/read` -- query tuples
- `POST /stores/{store_id}/check` -- test permissions

With mesh-based isolation (Open Question #7), tenant admins authenticate with their JWT and the mesh restricts them to their store's URL paths. No custom admin API or additional infrastructure is needed -- OpenFGA's API is the admin interface.

The **OpenFGA Playground** (enabled at port 3000, port-forwarded to localhost:15007) provides a visual UI for exploring models and testing permissions during development.

---

## File Structure (revised)

```
# Platform-level (shared infrastructure, no policy)
platform/manifests/
  openfga.yaml                # OpenFGA server + openfga-envoy deployment
platform/values/
  openfga.yaml                # OpenFGA Helm values

# Vendored openfga-envoy (patched for per-request tenant resolution)
vendor/openfga-envoy/
  extauthz/
    cmd/extauthz/main.go      # Entry point (passes store name config)
    internal/server/
      authz/authz.go           # Core patch: resolveClient, resolveStoreIDByName, client cache
      config/config.go          # Added StoreIDHeader, StoreNameHeader fields
    Dockerfile                  # Multi-stage build (golang:1.22-alpine)
scripts/
  07-configure-openfga.sh     # Deploy + health-check (no stores, no models)

# Example-level (example-specific policy)
examples/07-tool-access-policy/
  PLAN.md                     # Original plan (CEL-based)
  PLAN-openfga.md             # This plan (OpenFGA-based)
  README.md                   # Updated walkthrough
  manifests.yaml              # K8s resources (updated policy with ext_authz)
  openfga-model.json          # Authorization model DSL (example-specific roles)
  setup-openfga.sh            # Create store + write model (example-specific)
  setup-tenant.sh             # DCR + OpenFGA tuple writes (example-specific)
  mcp-server/
    Dockerfile
    server.py                 # MCP server with 4 tools
    requirements.txt
```

---

## Future: Tenant Onboarding Controller

The current approach uses manual scripts for tenant onboarding (`setup-openfga.sh`, `setup-tenant.sh`) and resolves store names to store_ids at runtime via `ListStores`. This works for the example and early stages, but has limitations at scale:

- OpenFGA store names are **not unique** -- duplicate names produce ambiguous lookups
- The `storeNameCache` in openfga-envoy has no TTL or invalidation
- Onboarding is a multi-step manual process across Keycloak + OpenFGA + K8s

### Target architecture

A **tenant onboarding controller** that watches for tenant creation events (e.g., a Tenant CRD or namespace with a label) and orchestrates the full provisioning flow:

1. **Keycloak:** Create client (DCR), create tenant group (`/tenants/<name>`), configure mappers
2. **OpenFGA:** Create store, write the tenant's authorization model (from a template or CRD)
3. **Keycloak group attribute:** Write the OpenFGA `store_id` back to the tenant group as an attribute (`openfga_store_id`). A Keycloak protocol mapper emits this as a `fga_store_id` JWT claim.
4. **K8s namespace:** Create namespace, apply NetworkPolicies, label for ambient mesh

### What changes from the current design

- **JWT gains `fga_store_id` claim:** Keycloak emits the store_id directly in the token via a group attribute mapper. No runtime resolution needed.
- **Waypoint `requestMetadata` switches:** From `x-fga-store-name: jwt.claims.tenant` to `x-fga-store-id: jwt.claims.fga_store_id`.
- **openfga-envoy simplifies:** The `store_name_header` / `resolveStoreIDByName` / `storeNameCache` code is no longer needed. Only the `store_id_header` path remains -- read header, pass to Check. Smaller patch, more upstream-contributable.
- **Store name ambiguity eliminated:** The store_id is a ULID (immutable, globally unique). The store name is kept for human readability but is no longer on the hot path.
- **Cache invalidation is a non-issue:** No cache to invalidate -- the store_id is in the token. Token staleness after store recreation is bounded by access token TTL (typically minutes), and STS exchange issues fresh tokens per-request.

### Migration path

The migration from the current approach is non-disruptive:

1. Add `openfga_store_id` group attribute + protocol mapper in Keycloak
2. Switch waypoint `requestMetadata` from `x-fga-store-name` to `x-fga-store-id`
3. Remove store name resolution code from openfga-envoy (or just stop setting the header)

No data migration, no flag day. Both `store_id_header` and `store_name_header` can coexist during transition.
