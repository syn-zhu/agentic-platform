# Example 07: Multi-Layer Access Policy Enforcement

## Overview

Demonstrates **three layers of authorization** for an agentic platform:

1. **Ingress authentication** -- every request through the gateway must carry a valid JWT with a tenant claim (rejects unauthenticated requests). No audience check -- the ingress is a transport layer, not a resource server.
2. **Agent access control** -- per-agent policy at the waypoint controlling which users can invoke which agents. Each agent requires its own audience in the token, using the raw K8s service account format `system:serviceaccount:<namespace>:<name>` (the agent is the resource server).
3. **Tool access control** -- per-tool policy on MCP calls combining agent identity (SPIFFE) + user identity (client roles via token exchange) (east-west, at the waypoint)

**Audience convention:** All audiences (for both agents and MCP servers) use the raw K8s service account format `system:serviceaccount:<namespace>:<name>` (e.g., `system:serviceaccount:example-tool-policy:policy-mcp-server`). This is derived from the K8s CR's namespace + name and matches the Keycloak `client_id` set during DCR. Using the full SA format unifies the OAuth audience with the infrastructure identity -- the Keycloak client IS the service account, via federated auth. Every workload (agent or MCP server) uses its SA as both its OAuth client identity (outbound) and its audience (inbound). The agent runtime derives the audience automatically from the tool reference in the Agent CR (no explicit audience field needed).

## Architecture

```
User (JWT) --> Ingress Gateway --> Agent --> [mesh mTLS] --> Waypoint --> MCP Server
                    |                |                          |
                    |                |                          |-- JWT validation (exchanged token)
                    |                |                          |-- CEL authorization:
                    |                |                          |     source.identity.serviceAccount (SPIFFE)
                    |                |                          |     jwt.resource_access (client role)
                    |                |                          |     mcp.tool.name
                    |                |
                    |                |-- STS exchange (Keycloak)
                    |                |   (audience-scoped token)
                    |                |
                    |                |-- Agent access policy:
                    |                      jwt.tenant, jwt.resource_access
                    |
                    |-- Ingress auth policy:
                          valid JWT + has(jwt.claims.tenant)
```

---

## Phase 0: Platform Prerequisites -- Keycloak Upgrade to 26.5

### 0.1 Switch to codecentric keycloakx Helm chart

The current bitnami chart (25.2.0) must be replaced with the codecentric keycloakx chart (7.1.8) which ships Keycloak 26.5.3. This enables:
- **Standard Token Exchange V2** (preview, enabled via feature flag)
- **Federated Client Authentication** (preview, enabled via feature flag)
- **Dynamic Client Registration** (RFC 7591)

Files to modify:
- `platform/values/keycloak.yaml` -- Rewrite for codecentric chart format (different value structure: `command`, `extraEnv`, `database.*`, etc.). Enable features via `--features=token-exchange,client-policies,dynamic-client-registration` in start command args. Point to existing RDS PostgreSQL.
- `scripts/06-configure-keycloak.sh` -- Update pod label selectors (codecentric uses `app.kubernetes.io/name=keycloakx`), update admin credential secret name.

### 0.2 Clean up and update realm configuration

The current `keycloak-agents-realm.json` is a mix of actively-used config and Keycloak boilerplate defaults. It needs cleanup and extension.

**What's actually used today:**
- `agent-gateway` client -- OIDC client for user authentication flows (standard flow, direct access grants). No longer used as an audience target -- the ingress gateway is a transport layer and does not check audience.
- `agentregistry` client -- OIDC validation for the AgentRegistry service
- `agentregistry-audience` mapper -- adds `agentregistry` to `aud` claim
- `tenant` mapper -- maps user attribute to `tenant` claim (referenced by JWT policy `has(jwt.claims.tenant)`)

**What's unused and should be removed:**
- `agent-gateway-audience` mapper -- was adding `agent-gateway` to `aud` claim for ingress audience validation, but ingress no longer checks audience. Audience validation now happens at the per-agent waypoint policy, where each agent requires its own audience (added via DCR per-agent client).

**What's unused boilerplate:**
- `offline_access` / `uma_authorization` roles -- Keycloak built-in defaults, not referenced by any platform component
- Client registration policies (Consent Required, Full Scope Disabled, etc.) -- Keycloak defaults
- Browser security headers -- Keycloak defaults

**Changes to make:**

1. **Enable user self-registration**: Set `registrationAllowed: true`
2. **Tenant group structure**: `/tenants` root group for multi-tenancy. Sub-groups created per-tenant at runtime (e.g., `/tenants/acme`). Groups serve as the FGAP V2 scoping boundary for delegated admin.
3. **FGAP V2 delegated admin**: Tenant admins get fine-grained permissions scoped to their tenant group. This lets them manage users and client role assignments within their tenant -- without platform admin involvement and without access to other tenants. FGAP V2 (Keycloak 26.2+) replaces the legacy all-or-nothing `manage-users` realm role.
4. **Enable Token Exchange**: `token-exchange-standard` is enabled by default in Keycloak 26.2+
5. **Configure K8s OIDC Identity Provider**: For federated client authentication -- allows K8s service account tokens as client assertions. The IdP uses the cluster's OIDC discovery endpoint (`https://oidc.eks.<region>.amazonaws.com/id/<cluster-id>`)
6. **Remove unused boilerplate**: Strip out `offline_access`/`uma_authorization` from `defaultRoles`, remove unused client registration policy components

**No realm roles in the platform realm.** Roles are service-specific (e.g., `analyst`, `operator`, `admin` are specific to the MCP server in Example 07, not universal platform concepts). Instead, roles are modeled as **client roles** on the Keycloak client registered for each MCP server via DCR. This means:
- Each MCP server defines its own role vocabulary (e.g., `analyst`, `reviewer`, `approver`)
- Roles appear in the token under `resource_access["system:serviceaccount:<ns>:<name>"].roles`
- Tenant admins assign client roles to users via admin API (self-serve, scoped by FGAP V2)
- The CEL policy reads `jwt.resource_access["system:serviceaccount:<ns>:<name>"].roles`

**Three independent concerns:**
- **Client roles** = what roles exist and how they appear in tokens (per-service)
- **FGAP V2** = who is allowed to assign those roles to whom (admin permissions)
- **Groups** = the boundary that FGAP V2 scopes admin permissions to (per-tenant)

**Why client roles instead of realm roles or groups-as-roles:**
- Realm roles are global -- no MCP server should dictate what roles exist for the entire platform
- Groups can only *assign* roles, not *define* them -- roles must be created somewhere first
- Client roles scope naturally to the service that defines them
- **TODO**: Migrate to Keycloak Organizations (org-scoped roles) when that feature ships (tracked in keycloak/keycloak#40585 and #43507). See also `platform/README.md` Auth Policies section.

**Important**: No example-specific roles, clients, or users in the platform realm. Only platform-level infrastructure (tenant group structure, FGAP V2, IdP configuration).

### 0.3 Configure Initial Access Token for DCR

Add to `scripts/06-configure-keycloak.sh`:
- Create an Initial Access Token (IAT) via Keycloak admin API: `POST /admin/realms/agents/clients-initial-access`
- Store the IAT as a K8s Secret in a well-known namespace (e.g., `platform-system`) that tenant setup scripts can reference
- The IAT authorizes Dynamic Client Registration requests (RFC 7591)

---

## Phase 1: Example Namespace Setup

### 1.1 Namespace with mesh + waypoint

Standard pattern from existing examples:
```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: example-tool-policy
  labels:
    platform.agentic.io/gateway-discovery: "true"
    platform.agentic.io/tenant: "true"
    istio.io/dataplane-mode: ambient
    istio.io/use-waypoint: agentgateway-waypoint
    istio.io/ingress-use-waypoint: "true"
```

Plus the standard RoleBinding, NetworkPolicy, AuthorizationPolicy (L4 SPIFFE), and waypoint Gateway.

### 1.2 Tenant Setup Script (`examples/07-tool-access-policy/setup-tenant.sh`)

Self-contained script demonstrating the self-serve tenant onboarding flow. No platform admin involvement required (beyond the one-time IAT creation in Phase 0.3).

**Step 1: User self-registration**
- Users register via Keycloak's public registration endpoint or the Keycloak registration UI
- On registration, users have no roles and no tenant affiliation
- For the example, the script registers `alice` and `bob` via the admin API

**Step 2: Tenant group setup** (one-time, done by platform admin for bootstrapping)
- Create tenant group tree under `/tenants/acme`
- Add `bob` to the tenant group as the initial tenant admin (this is the one privileged step)
- Set the `tenant` user attribute on both users to `acme`

**Step 3: Register the MCP server's client via DCR**
- Read the IAT from the platform Secret (`platform-system/keycloak-initial-access-token`)
- POST to `/realms/agents/clients-registrations/openid-connect` with:
  - `client_id`: `system:serviceaccount:${NAMESPACE}:${MCP_SERVER_NAME}` (e.g., `system:serviceaccount:example-tool-policy:policy-mcp-server`)
  - `client_name`: same as `client_id` for display purposes
- The raw SA format unifies the OAuth client identity with the K8s infrastructure identity and matches the audience the agent runtime derives from the tool reference
- This client is the **audience / resource server** -- it defines what roles exist for this MCP server and is the target audience for exchanged tokens
- Store the returned `client_id` and `registration_access_token` as a K8s Secret

**Step 4: Create client roles on the MCP server's client**
- Via admin API, create roles on the `system:serviceaccount:example-tool-policy:policy-mcp-server` client: `analyst`, `operator`, `admin`
- These roles are specific to this MCP server -- other MCP servers define their own role vocabulary
- This is self-serve: the MCP server owner defines what roles their service understands

**Step 5: Register the agent's client via DCR**
- POST to `/realms/agents/clients-registrations/openid-connect` with:
  - `client_id`: `system:serviceaccount:${NAMESPACE}:${AGENT_NAME}` (e.g., `system:serviceaccount:example-tool-policy:tool-policy-agent`)
  - `client_name`: same as `client_id` for display purposes
  - `grant_types`: `["urn:ietf:params:oauth:grant-type:token-exchange"]`
  - `token_endpoint_auth_method`: `private_key_jwt` (for federated auth with K8s SA token)
- This client is the **token exchange client** -- the agent authenticates as this client (via K8s SA token) when exchanging the user's JWT for an audience-scoped token
- Store the returned `client_id` and `registration_access_token` as a K8s Secret in the example namespace

**Step 6: Assign client roles to users**
- Assign `alice` the `analyst` client role on `system:serviceaccount:example-tool-policy:policy-mcp-server`
- Assign `bob` the `admin` client role on `system:serviceaccount:example-tool-policy:policy-mcp-server`
- Token will carry: `resource_access: {"system:serviceaccount:example-tool-policy:policy-mcp-server": {"roles": ["analyst"]}}`
- Later, promote `alice` to `operator` by changing her client role assignment

**Step 7: Configure token exchange permission**
- Via admin API, grant the agent's client (`system:serviceaccount:example-tool-policy:tool-policy-agent`) permission to exchange tokens for the MCP server's audience (`system:serviceaccount:example-tool-policy:policy-mcp-server`)

---

## Phase 2: MCP Server

### 2.1 Deploy MCP server via kmcp

A minimal Python MCP server (StreamableHTTP) with role-differentiated tools, deployed via the `MCPServer` CRD (kmcp operator creates Deployment + Service automatically with `appProtocol: kgateway.dev/mcp`):

| Tool | Description | Intended Access |
|------|-------------|----------------|
| `list_reports` | List available reports | Any tenant member (`analyst`, `operator`, or `admin`) |
| `read_report` | Read a specific report | Any tenant member (`analyst`, `operator`, or `admin`) |
| `execute_query` | Run an arbitrary database query | `operator` or `admin` role only |
| `modify_config` | Change system configuration | `admin` role only + specific agent only |

```yaml
apiVersion: kagent.dev/v1alpha1
kind: MCPServer
metadata:
  name: policy-mcp-server
  namespace: example-tool-policy
spec:
  transportType: http
  httpTransport:
    targetPort: 3000
    path: /mcp
  deployment:
    image: <registry>/policy-mcp-server:latest
    port: 3000
    env:
      HOST: "0.0.0.0"
```

### 2.2 AgentgatewayBackend (static target) + HTTPRoute

```yaml
apiVersion: agentgateway.dev/v1alpha1
kind: AgentgatewayBackend
metadata:
  name: policy-mcp
  namespace: example-tool-policy
spec:
  mcp:
    targets:
      - name: policy-mcp-server
        static:
          host: policy-mcp-server.example-tool-policy.svc.cluster.local
          port: 3000
          path: /mcp
          protocol: StreamableHTTP
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: policy-mcp
  namespace: example-tool-policy
spec:
  hostnames:
    - policy-mcp-server.example-tool-policy.svc.cluster.local
  parentRefs:
    - name: agentgateway-waypoint
  rules:
    - backendRefs:
        - group: agentgateway.dev
          kind: AgentgatewayBackend
          name: policy-mcp
```

**Why an HTTPRoute is needed:** Without an explicit HTTPRoute, the waypoint's `_waypoint-default` passthrough route resolves the destination as a `BackendReference::Service`, which goes through the generic `Backend::Service` HTTP proxy path. MCP-specific authentication and authorization (from the AgentgatewayPolicy) only execute in the `Backend::MCP` code path, which is only reached when an HTTPRoute routes traffic to an AgentgatewayBackend with an MCP spec. The HTTPRoute overrides the default passthrough so that traffic flows through the MCP handler where policies are enforced.

**Verification step:** During implementation, try deploying without the HTTPRoute first to confirm this behavior. If the waypoint applies MCP policies via the default passthrough (unlikely based on code analysis), the HTTPRoute can be removed. This is also a candidate discussion topic for the AgentGateway community -- whether the waypoint could automatically associate backends with services without explicit routes.

Note: The kmcp-created Service currently does not set metadata labels, so the selector-based approach cannot match it. Using `static` target as a workaround. Upstream fix to kmcp (propagate labels to Service) is a followup.

### 2.3 AgentgatewayPolicy (JWT auth + CEL authorization)

```yaml
apiVersion: agentgateway.dev/v1alpha1
kind: AgentgatewayPolicy
metadata:
  name: tool-access-policy
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
        issuer: "https://keycloak.example.com/realms/agents"
        audiences:
          - "system:serviceaccount:example-tool-policy:policy-mcp-server"
        jwks:
          remote:
            url: "https://keycloak.example.com/realms/agents/protocol/openid-connect/certs"
      authorization:
        policy:
          matchExpressions:
            # Rule 1: Any tenant member (analyst/operator/admin) can list/read reports
            - 'mcp.tool.name in ["list_reports", "read_report"] && ("analyst" in jwt.resource_access["system:serviceaccount:example-tool-policy:policy-mcp-server"].roles || "operator" in jwt.resource_access["system:serviceaccount:example-tool-policy:policy-mcp-server"].roles || "admin" in jwt.resource_access["system:serviceaccount:example-tool-policy:policy-mcp-server"].roles)'
            # Rule 2: operator or admin can execute queries
            - 'mcp.tool.name == "execute_query" && ("operator" in jwt.resource_access["system:serviceaccount:example-tool-policy:policy-mcp-server"].roles || "admin" in jwt.resource_access["system:serviceaccount:example-tool-policy:policy-mcp-server"].roles)'
            # Rule 3: Only admin + specific agent can modify config
            - 'mcp.tool.name == "modify_config" && "admin" in jwt.resource_access["system:serviceaccount:example-tool-policy:policy-mcp-server"].roles && source.identity.serviceAccount == "tool-policy-agent"'
```

Key points:
- `matchExpressions` uses OR semantics -- any expression matching allows the request
- `source.identity.serviceAccount` comes from SPIFFE via mesh mTLS (agent identity)
- `jwt.resource_access["system:serviceaccount:example-tool-policy:policy-mcp-server"].roles` comes from client roles on the DCR-registered client (user identity)
- `mcp.tool.name` is the specific tool being called/listed (tool-level filtering applies to both `list_tools` and `call_tool`)

### 2.4 Agent access policy (at waypoint)

Controls which callers can invoke the agent. This policy is enforced at the tenant's **waypoint**, not the ingress gateway -- making it apply uniformly to both north-south (user to agent) and east-west (agent to agent via A2A) traffic. This is a property of the agent, not of how you reach it.

Requires an HTTPRoute in the tenant namespace that routes traffic to the agent's Service through the waypoint:

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: tool-policy-agent-access
  namespace: example-tool-policy
spec:
  hostnames:
    - tool-policy-agent.example-tool-policy.svc.cluster.local
  parentRefs:
    - name: agentgateway-waypoint
  rules:
    - backendRefs:
        - name: tool-policy-agent
          port: 8080
---
apiVersion: agentgateway.dev/v1alpha1
kind: AgentgatewayPolicy
metadata:
  name: agent-access-policy
  namespace: example-tool-policy
spec:
  targetRefs:
    - group: gateway.networking.k8s.io
      kind: HTTPRoute
      name: tool-policy-agent-access
  traffic:
    jwtAuthentication:
      mode: Strict
      providers:
        - issuer: "http://keycloak.keycloak.svc.cluster.local:8080/realms/agents"
          audiences:
            - "system:serviceaccount:example-tool-policy:tool-policy-agent"
          jwks:
            remote:
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
          # Only users in tenant "acme" can call this agent
          - 'jwt.claims.tenant == "acme"'
```

**Three distinct policy layers, each at a different scope:**
- **Ingress auth policy** (platform-wide, at ingress gateway): is the request authenticated at all? Valid JWT with issuer + `has(jwt.claims.tenant)`. No audience check -- the ingress is a transport layer.
- **Agent access policy** (per-agent, at waypoint): can this caller invoke this specific agent? Audience must match the agent (`system:serviceaccount:example-tool-policy:tool-policy-agent`) + tenant check.
- **Tool access policy** (per-tool, at waypoint): can this user call this specific tool via this agent? (client role + agent identity)

For this example we demonstrate the agent access policy with a JWT tenant check (north-south). A2A access control (east-west, using SPIFFE identity) is a followup.

---

## Phase 3: Agent with Token Exchange

### 3.1 Agent CR

Uses the v1alpha2 `Declarative` agent type. Key differences from earlier draft: `type: Declarative` with `declarative:` block containing `modelConfig`, `systemMessage`, `tools[]`, `a2aConfig`, and `deployment`.

```yaml
apiVersion: kagent.dev/v1alpha2
kind: Agent
metadata:
  name: tool-policy-agent
  namespace: example-tool-policy
  annotations:
    platform.agentic.io/expose: "true"
spec:
  description: "Agent demonstrating multi-layer access policy enforcement"
  type: Declarative
  declarative:
    modelConfig: default-model-config
    systemMessage: |
      You are a data operations assistant. ...
    tools:
      - type: McpServer
        mcpServer:
          name: policy-mcp-server
          kind: MCPServer
          apiGroup: kagent.dev
          allowedHeaders:
            - Authorization
    a2aConfig:
      skills:
        - id: secure-data-ops
          name: Secure Data Operations
          description: "Access reports, execute queries, and manage configuration with RBAC"
    deployment:
      env:
        - name: STS_WELL_KNOWN_URI
          value: "http://keycloak.keycloak.svc.cluster.local:8080/realms/agents/.well-known/openid-configuration"
```

Key points:
- `allowedHeaders: ["Authorization"]` on the tool enables the agent to propagate the exchanged token to MCP tool calls. STS-generated Authorization headers take precedence over request headers.
- `STS_WELL_KNOWN_URI` env var triggers the full token exchange pipeline in the ADK runtime.
- No `STS_CLIENT_ID` or `STS_AUDIENCE` env vars -- the agent authenticates via K8s SA token (federated client auth), and the audience for the exchanged token is derived from the tool reference (`system:serviceaccount:<namespace>:<name>`) at runtime.

The `STS_WELL_KNOWN_URI` triggers the full token exchange pipeline:
1. `ADKTokenPropagationPlugin.before_run_callback` extracts the user's JWT from the incoming request headers
2. For each MCP toolset, the plugin derives the audience from the tool's MCPServer reference (`system:serviceaccount:<namespace>:<name>`)
3. `ADKSTSIntegration.exchange_token(audience="system:serviceaccount:example-tool-policy:policy-mcp-server")` sends RFC 8693 token exchange to Keycloak, using the K8s SA token as `actor_token`
4. Keycloak's token exchange policy validates the audience and scopes the returned token accordingly, retaining the user's client roles
4. `header_provider` injects `Authorization: Bearer <exchanged-token>` on outbound MCP calls
5. The waypoint validates this token and evaluates CEL rules

### 3.2 Federated Client Authentication

The agent authenticates to Keycloak's token endpoint using its K8s service account token (projected volume at `/var/run/secrets/tokens/kagent-token`) as a `client_assertion` of type `urn:ietf:params:oauth:client-assertion-type:jwt-bearer`. This is handled by the `ActorTokenService` in `agentsts-core`.

No `client_secret` is stored. The K8s SA token is cryptographically verifiable by Keycloak via the EKS OIDC provider configured in Phase 0.2.

---

## Phase 4: README and Walkthrough

### 4.1 File structure

```
examples/07-tool-access-policy/
  README.md
  manifests.yaml          # All K8s resources
  setup-tenant.sh         # DCR + user registration script
  mcp-server/
    Dockerfile
    server.py             # Simple MCP server with 4 tools
    requirements.txt
```

### 4.2 README content outline

1. **What this demonstrates** -- three layers of access control for an agentic platform
2. **Prerequisites** -- platform with Keycloak 26.5, cluster OIDC configured
3. **Setup** -- run `setup-tenant.sh`, then `kubectl apply -f manifests.yaml`
4. **Walkthrough**:
   - **Layer 1: Ingress authentication** -- curl the agent without a token, get 401 (rejected by ingress auth policy)
   - **Layer 2: Agent access control** -- `alice` (tenant=acme) can call the agent; a user from a different tenant gets 403 (rejected by agent access policy at waypoint)
   - **Layer 3: Tool access control**:
     - `alice` and `bob` are registered, MCP server client created with roles `analyst`, `operator`, `admin`
     - `alice` with `analyst` role can list/read reports, but `execute_query` is denied
     - `alice` promoted to `operator` -- can now also execute queries
     - `bob` with `admin` role can modify config via the `tool-policy-agent`
     - A different agent (not `tool-policy-agent`) with an admin user still cannot `modify_config` -- agent identity check fails
5. **How it works** -- three policy layers (ingress auth, agent access, tool access), token exchange flow, SPIFFE identity, client roles, CEL evaluation
6. **Production considerations** -- controller-based DCR, token caching, A2A agent access control, migration to org-scoped roles

---

## Upstream Change: Per-Tool Audience in agentsts-adk

**Repo:** `kagent-dev/kagent` -- `python/packages/agentsts-adk/`

**Problem:** The `ADKTokenPropagationPlugin` (in `agentsts/adk/_base.py`) currently does a single token exchange per invocation in `before_run_callback`, with no `audience` parameter. This works for agents that talk to a single MCP server (Keycloak's server-side policy picks the audience), but breaks when an agent talks to multiple MCP servers with different audiences.

**Proposed change:** Move the token exchange from `before_run_callback` (once per invocation) to `header_provider` (called per MCPToolset), passing the audience derived from the toolset's MCPServer reference.

**Key changes:**

1. **`ADKTokenPropagationPlugin.add_to_agent`** -- when iterating MCPToolsets, store the audience (`system:serviceaccount:<namespace>:<name>`) alongside each toolset's `_header_provider` closure.

2. **`header_provider`** -- on each call, check if a cached token exists for this audience. If not (or expired), call `exchange_token(audience=<sa-identity>)`. Cache by audience key.

3. **`before_run_callback`** -- still extracts the subject token from headers, but no longer does the exchange. Stores the subject token for `header_provider` to use.

4. **ADK translator** (Go, `adk_api_translator.go`) -- must pass the MCPServer's SA identity (`system:serviceaccount:<namespace>:<name>`) as metadata that the Python ADK runtime can read when constructing MCPToolset objects. This is how the audience value gets from the Agent CR into the Python plugin.

**Token cache changes:**
- Current: single token cached per session ID
- Proposed: token cached per `(session_id, audience)` tuple

**Why `header_provider` instead of `before_tool_callback`:**
- `header_provider` is already per-MCPToolset (set in `add_to_agent` line 68-69)
- Each MCPToolset gets its own closure, so the audience can be bound at setup time
- No need for a new callback or hook

This upstream change must land before Phase 3 of this example can work with the SA-format audience convention.

---

## Implementation Order

**Process**: Implement one phase at a time. Pause after each phase and wait for user confirmation before proceeding to the next.

1. **Phase 0** (platform): Keycloak upgrade + realm config + IAT setup
2. **Phase 2.1** (example): MCP server implementation (container image + MCPServer CR)
3. **Phase 1** (example): Namespace + standard resources
4. **Phase 2.2-2.4** (example): AgentgatewayBackend + MCP tool policy + agent access policy
5. **Upstream change** (kagent): Per-tool audience in agentsts-adk + ADK translator audience passthrough
6. **Phase 3** (example): Agent CR + token exchange config
7. **Phase 1.2** (example): Tenant setup script (DCR with SA format as `client_id`)
8. **Phase 4** (example): README + walkthrough testing

---

## Open Questions / Risks

1. **Keycloak migration**: Moving from bitnami to codecentric chart is a database migration. Need to verify schema compatibility between Keycloak 25.2 and 26.5. May need to run Keycloak's built-in migration (`kc.sh start --optimized` handles auto-migration).

2. **`STS_CLIENT_ID` env var**: Need to verify this is the correct env var name that `agentsts-core` reads for the client_id during token exchange. The code uses `ActorTokenService` for the actor token, but the client_id for the exchange request may need a separate config mechanism.

3. **Federated auth support in agentsts-core**: Need to verify that `ActorTokenService` (which reads the K8s SA token) is used as `client_assertion` in the token exchange request, not just as `actor_token`. The RFC 8693 exchange has both concepts -- client authentication (how the client proves its identity) vs. actor token (who is acting on behalf of whom).

4. **CEL `in` operator for nested claims**: Need to verify that `"operator" in jwt.resource_access["system:serviceaccount:example-tool-policy:policy-mcp-server"].roles` works correctly in AgentGateway's CEL implementation. The nested map access with `:` in the key + list membership check needs testing.

5. **kmcp Service labels**: The kmcp-created Service has no metadata labels, preventing use of selector-based `AgentgatewayBackend` targets. Using `static` target as workaround. Upstream fix to kmcp is a followup.

6. **Per-tool audience in agentsts-adk**: The `ADKTokenPropagationPlugin` currently exchanges the token once per invocation (in `before_run_callback`) with no audience parameter -- the audience is determined server-side by Keycloak. For the SA-format audience convention to work, the plugin must be updated to exchange per-MCPToolset with the audience derived from the tool reference. This is an upstream change to `kagent/python/packages/agentsts-adk`. See "Upstream Change: Per-Tool Audience in agentsts-adk" section above.

7. **Routeless MCP policy enforcement**: The current AgentGateway waypoint requires an explicit HTTPRoute to route traffic through an AgentgatewayBackend (and thus apply MCP policies). Without the route, the `_waypoint-default` passthrough bypasses the MCP handler entirely. A potential enhancement to AgentGateway would be automatic backend association (e.g., via `appProtocol: kgateway.dev/mcp` on the Service port) so that MCP policies apply without explicit routes. This is a candidate topic for the AgentGateway community meeting.
