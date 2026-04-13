# Mycelium TODO

Items to revisit during implementation. Check off when resolved.

## Testing Layers
- [ ] **Unit tests** (local, fast) — pure function tests with assertions in code. Run during development. Currently: CRD type tests + generator tests. No golden files — assertions inline.
- [ ] **envtest integration tests** (CI, optionally local) — real API server + etcd, no kubelet. Tests CRD validation markers (ExactlyOneOf, CEL rules, patterns), controller reconciliation loops with real server-side apply semantics. Add when building reconcilers (Phase 3).
- [ ] **Chainsaw e2e tests** (deployment pipeline, real cluster) — declarative YAML-based tests. Apply Mycelium CRDs → assert generated AGW/Knative resources exist with correct spec. Run against test cluster. Add when reconcilers are functional. See ~/chainsaw for framework, /Users/siyanzhu/agentic-platform/tests/e2e/waypoint-egress/ for example.
- [ ] **Deployer-style golden tests** (optional, CI) — input CRD YAML → full reconciliation → compare multi-resource output against golden YAML. Useful for catching unintended output drift. Consider adding alongside envtest. Follow AGW pattern: `REFRESH_GOLDEN=true` to regenerate.

## Helm Packaging + CRD Dependencies
- [ ] **Package as Helm chart** with dependencies on AgentGateway, Knative Serving, and agent-sandbox declared in `Chart.yaml`. Helm installs deps first, ensuring required CRDs exist before our controller starts.
- [ ] **Controller startup check** as a safety net — verify required CRDs (Gateway API, AgentGateway, Knative Serving, agent-sandbox) exist at startup, fail fast with clear error if missing.

## AgentGateway Deployment
- [ ] **Project reconciler must create the per-namespace AgentGateway deployment.** Currently we generate AGW policies, routes, and backends, but there's no actual Gateway resource being created for them to attach to. The reconciler needs to generate a `Gateway` resource (with the `agentgateway` GatewayClass) in the project's namespace, including the external (443 HTTPS) and internal (8080 HTTP) listeners. Without this, all the generated policies have nowhere to point. This is also where the Mycelium Engine sidecar gets deployed alongside the AGW proxy.

## Define kata runtime class and make configurable
First, the knative generate function reference kata-fc but we haven't actually defined that yet.
Second, we should probably support a few different runtime classes and allow that to be specified in the tool definition
Same with the agent sandboxes

## Consider using kmcp
See if we can completely avoid any MCP implementation in the engine; in theory we should be able to determine the
tools/list response entirely from code, so maybe see if something like kmcp auto-handles the response for us
or even better, maybe agentgateway already does this. Otherwise, we'd have to push changes from the mycelium controller
to the engine for it to become aware of new tools (similar to WDS / xDS pattern)


## Status Conditions
Review and clean up the Status Conditions for every CRD
Roughly speaking, every "owned" resource (i.e. ones which would get garbage-collected upon deletion) should be included in 
the Status field of each CRD, and hence we should also have a Status condition associated with it
Make sure to update all the CRD field comments / descriptions as well
- [ ] **Project**: `AGWResourcesReady` — set after all 5 AGW resources are successfully applied via SSA. Currently only `Ready` and `NamespaceReady` are set.
- [ ] **Tool**: `ServiceReady` — set when the generated Knative Service is available and healthy. Currently documented but not set by the reconciler.
- [ ] **Tool**: `CredentialsValid` — set when the referenced CredentialProvider(s) exist and are Ready. Currently documented but not set by the reconciler.
- [ ] **CredentialProvider**: `SecretValid` — validate that the referenced K8s Secret (clientSecretRef / apiKeySecretRef) actually exists. Currently not checked.
- [ ] **Agent**: `ServiceAccountReady` — set when the per-agent K8s ServiceAccount is created. Depends on agent-sandbox integration.
- [ ] **Agent**: `SandboxReady` — set when the SandboxTemplate + WarmPool are generated and healthy. Depends on agent-sandbox integration.

## Field Indexes
- [ ] **Tool → CredentialProvider index**: Add field index on `spec.credentials.providerRefs` that returns all referenced CredentialProvider names (OAuth + API keys). Enables O(1) lookup in the CredentialProvider deletion webhook instead of listing all Tools and filtering in code. Register via `mgr.GetFieldIndexer().IndexField()` at manager startup.
- [ ] **Agent → Tool index**: Similar index on `spec.tools[].ref.name` for the Tool deletion webhook. Enables O(1) lookup of Agents referencing a given Tool.
- [ ] Consider indexes for any other cross-resource lookups that become hot paths at scale.

## CRD 
Especially the Tool Input schema; definitely need to add validations for that
- [ ] Deep review of all CRD validations — cover every edge case with envtest (empty strings, max-length strings, boundary values for scaling, invalid patterns, etc.)
- [ ] Test that `ExactlyOneOf` rejects invalid combinations at admission time (envtest)
- [ ] Test `minScale <= maxScale` XValidation with envtest
- [ ] Test item-level XValidation CEL rules (audience length, scope length, etc.)
- [ ] Evaluate whether `InputSchema` needs a size bound (CEL `size(string(self)) <= 32768` or similar)
- [ ] Consider defining shared string type aliases (TinyString, ShortString, URLString) like AgentGateway if marker repetition becomes a maintenance burden

## Name every child resource relative to parent
To avoid name conflicts, we should name each child resource that we create relative to the parent.
We should figure out whether to have different values for Field Owner (controller) vs managed-by label. 

## Define additional listener for egress
We might wanna have three separate sections on the gateway: ingress, internal, and egress
We can rely on DNS to make sure egress routes to different port; or actually can we? maybe we additionally have to use Cilium...
Also, there's a number of hardcoded stuff (like port) in agw.go, consider if we should extract these out or get them passed in
also, ProjectLabels function is inconsistent with every other type which just adds it inline
However, since we already have actual Owner references, we should make these "mycelium.io/..." into annotations instead of labels
Also, the MCPBackend should just point at the Engine locally via UDS, and we should get rid of engine_service.go and all references.

## CredentialProvider
- [x] ~~`callbackUrl` in status~~ — **Resolved:** callback URL is deterministic (`{tenant-gateway-base}/oauth2/callback/{credentialprovider-name}`), no CRD field needed. Returned in the API response when creating an OAuth CredentialProvider.
- [ ] Deletion protection via finalizer — controller should add a finalizer to CredentialProviders that are referenced by Tools, and block deletion while dependents exist. Implement in the controller reconciliation loop.

## MongoDB Architecture (per-cell platform MongoDB)
- [ ] **Per-tenant database isolation:** Each tenant gets their own database (`tenant-{name}`) in the cell's platform MongoDB. Contains: `encryption.__keyVault` (tenant's DEKs only), `oauth_tokens`, `api_keys` (both CSFLE-encrypted). No shared key vault collection — complete database-level isolation.
- [ ] **Per-tenant credentials:** Controller provisions a scoped MongoDB user at tenant onboarding with `readWrite` on `tenant-{name}` database only. Credential stored as K8s Secret in tenant namespace, mounted by the engine sidecar.
- [ ] **KMS configuration:** Each tenant can bring their own KMS (AWS KMS, Azure Key Vault, GCP KMS). Need to decide where KMS config lives — TenantConfig? Separate CRD? Operator flag?
- [ ] **DEK provisioning:** Controller creates the tenant's DEK (encrypted with their CMK) during onboarding. Stored in `tenant-{name}.encryption.__keyVault`.
- [ ] **Collection design:** Decide on `oauth_tokens` + `api_keys` (separate collections) vs single `credentials` collection with type discriminator. Separate collections lean cleaner (different schemas, different lifecycle).
- [ ] **Platform MongoDB deployment:** How is the per-cell MongoDB itself deployed? Atlas? Self-managed? Operator-managed? Out of scope for Mycelium but need to document the prerequisite.

## Pod-to-Session Mapping (OQ-2)
- [ ] Resolve who owns the mapping (engine informer cache vs sandbox operator vs optimistic FQDN approach). See detailed notes in spec under OQ-2.
- [ ] Investigate whether agent-sandbox's SandboxClaim → Service lifecycle gives us safe-by-construction outbound routing via deterministic Service FQDNs.
- [ ] Consider whether claim creation should be a discrete control-plane step (like agent-sandbox examples) vs inline on first request in the engine.

## AGW Native MCP
- [ ] Verify AGW `backend.mcp.authorization` CEL expressions work with `source.workload.unverified.serviceAccount` for agent identity — need a running AGW to test this.
- [ ] Verify AGW filters `tools/list` responses based on tool-access policy — confirm the engine can serve all tools and AGW scopes the response per agent.
- [ ] Determine how the engine receives agent identity from AGW — transformation-injected headers vs source IP vs other mechanism.

## Knative
- [ ] Verify Knative Serving compatibility with Istio ambient mesh (OQ-5)
- [ ] Verify `runtimeClassName: kata-fc` works with Knative pod templates
- [ ] Test scale-to-zero and cold start latency for tool executors
- [ ] Investigate Knative func templates as reference for tool developer SDK

## Spec / Plan Sync
- [ ] Update Magenta-auth.md to reflect: ToolConfig → Tool rename, OAuthResource → CredentialProvider, credentials model (OAuth + API keys), AGW native MCP, Knative for tool executors
- [ ] Update implementation plan to reflect CRD changes
