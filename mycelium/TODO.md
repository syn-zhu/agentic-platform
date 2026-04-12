# Mycelium TODO

Items to revisit during implementation. Check off when resolved.

## CRD Validation
- [ ] Deep review of all CRD validations — cover every edge case with tests (empty strings, max-length strings, boundary values for scaling, invalid patterns, etc.)
- [ ] Test that `ExactlyOneOf` rejects invalid combinations at admission time (requires envtest or real API server)
- [ ] Test `minScale <= maxScale` XValidation with envtest
- [ ] Test item-level XValidation CEL rules (audience length, scope length, etc.)
- [ ] Evaluate whether `InputSchema` needs a size bound (CEL `size(string(self)) <= 32768` or similar)
- [ ] Consider defining shared string type aliases (TinyString, ShortString, URLString) like AgentGateway if marker repetition becomes a maintenance burden

## CredentialProvider
- [x] ~~`callbackUrl` in status~~ — **Resolved:** callback URL is deterministic (`{tenant-gateway-base}/oauth2/callback/{credentialprovider-name}`), no CRD field needed. Returned in the API response when creating an OAuth CredentialProvider.
- [ ] Deletion protection via finalizer — controller should add a finalizer to CredentialProviders that are referenced by Tools, and block deletion while dependents exist. Implement in the controller reconciliation loop.

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
