# Mycelium TODO


// TODO: should we be initializing list with condition state?
		// or, maybe we should compare our own last observed timestamp vs the actual
		// conditions' observed timestamps?
		// observed generation might not mean much if this is a reference
		// 

LastTransitionTime !! we should be using this
// While machine is deleting, treat unknown conditions from external objects as info (it is ok that those objects have been deleted at this stage).
		conditions.GetPriorityFunc(func(condition metav1.Condition) conditions.MergePriority {
			if !c.machine.DeletionTimestamp.IsZero() {
				if condition.Type == clusterv1.MachineBootstrapConfigReadyCondition && (condition.Reason == clusterv1.MachineBootstrapConfigDeletedReason || condition.Reason == clusterv1.MachineBootstrapConfigDoesNotExistReason) {
					return conditions.InfoMergePriority
				}
				if condition.Type == clusterv1.MachineInfrastructureReadyCondition && (condition.Reason == clusterv1.MachineInfrastructureDeletedReason || condition.Reason == clusterv1.MachineInfrastructureDoesNotExistReason) {
					return conditions.InfoMergePriority
				}
				if condition.Type == clusterv1.MachineNodeHealthyCondition && (condition.Reason == clusterv1.MachineNodeDeletedReason || condition.Reason == clusterv1.MachineNodeDoesNotExistReason) {
					return conditions.InfoMergePriority
				}
				// Note: MachineNodeReadyCondition is not relevant for the summary.
			}
			return conditions.GetDefaultMergePriorityFunc(c.negativePolarityConditionTypes...)(condition)
		}),


check deletion timestamp

Items to revisit during implementation. Check off when resolved.
if err != nil {
    if apierrors.IsNotFound(err) {
        // Permanent — resource doesn't exist, don't retry
        return ctrl.Result{}, nil
    }
    if apierrors.IsConflict(err) {
        // Optimistic concurrency — immediate retry will likely work
        return ctrl.Result{Requeue: true}, nil
    }
    if apierrors.IsForbidden(err) {
        // RBAC issue — retrying won't help, but maybe someone
        // fixes the role binding. Log loudly, let backoff retry.
        log.Error(err, "RBAC error — check controller permissions")
        return ctrl.Result{}, err
    }
    // Everything else — unknown, just retry with backoff
    return ctrl.Result{}, err
}
for project controller, should be typedObjectref not local because project has no namespace
TODO:
controller should be daemonset
How to prevent deletion of ownd resources (e.g. namespce)?
is NotFound actually terminal for our usecase?
controller.Base should have proper constructor
we should still use patchHelper for created child resources, but a separate one for each 
refactor indexers: webhooks refer to it directly by string. follow capsule pattern
check the setupWithManager for every reconciler
* (owns, watches, etc) and all the index logic; make it most efficient
add a controller for Secrets, and maybe a label? to avoid secrets being deleted when still used etc
add finalizer if not exist

make different conditions and reasons for every CRD, and set withownedconditions accordingly
WithOwnedConditions{Conditions: []string{
				v1alpha1.ReadyCondition,
			}},
 3. Preventing namespace deletion explicitly: This is the scary case — someone kubectl delete ns would cascade everything inside. You could add a ValidatingWebhook that rejects DELETE on namespaces labeled managed-by: mycelium-controller.     
  That's the only real guard against it. 

move finalizer label constants
withownerlabels or whatever is in the defer?
* what other things should we set
check webhooks (eficient, could we not get one by one?)

IsNotFound, what other errors are there
maybe webhook should also insert the "Pending"

update all webhook checks to have the "unless deleting" caveat

 The one related mechanism is cache.ByObject with field/label selectors, which restricts what the informer cache stores at all (i.e., reduces the watch scope at the API server level). But that's a cache-wide restriction, not per-reconciler, so
   it's only useful when you want to globally ignore a subset of objects (e.g., "only cache Secrets in namespaces that are Mycelium projects"). It's a different tool for a different problem.

  Builder methods
                                                                                                                                                                                                                                                    
  ┌─────────────────────────────────────────────────┬───────────────────────────────────────────────────────────────────┐
  │                     Method                      │                            Description                            │                                                                                                                           
  ├─────────────────────────────────────────────────┼───────────────────────────────────────────────────────────────────┤
  │ For(obj, ...ForOption)                          │ Primary reconciled type. Implicitly uses EnqueueRequestForObject. │
  ├─────────────────────────────────────────────────┼───────────────────────────────────────────────────────────────────┤
  │ Owns(obj, ...OwnsOption)                        │ Watch owned objects; enqueues the owner.                          │                                                                                                                           
  ├─────────────────────────────────────────────────┼───────────────────────────────────────────────────────────────────┤                                                                                                                           
  │ Watches(obj, handler, ...WatchesOption)         │ Watch any type with a custom handler.                             │                                                                                                                           
  ├─────────────────────────────────────────────────┼───────────────────────────────────────────────────────────────────┤                                                                                                                           
  │ WatchesMetadata(obj, handler, ...WatchesOption) │ Like Watches but forces metadata-only projection.                 │                                                                                                                           
  ├─────────────────────────────────────────────────┼───────────────────────────────────────────────────────────────────┤                                                                                                                           
  │ WatchesRawSource(src)                           │ Low-level: raw source.TypedSource. Skips global WithEventFilter.  │
  └─────────────────────────────────────────────────┴───────────────────────────────────────────────────────────────────┘                                                                                                                           
                                                  
  ---                                                                                                                                                                                                                                               
  Per-watch options (ForOption / OwnsOption / WatchesOption)
                                                                                                                                                                                                                                                    
  ┌─────────────────────────────┬─────────────────────────────────────────────────────────────────────────────┐
  │           Option            │                                 Description                                 │                                                                                                                                     
  ├─────────────────────────────┼─────────────────────────────────────────────────────────────────────────────┤
  │ builder.WithPredicates(...) │ Attach predicates scoped to this specific watch.                            │
  ├─────────────────────────────┼─────────────────────────────────────────────────────────────────────────────┤
  │ builder.OnlyMetadata        │ Cache only PartialObjectMetadata, not the full object.                      │                                                                                                                                     
  ├─────────────────────────────┼─────────────────────────────────────────────────────────────────────────────┤                                                                                                                                     
  │ builder.MatchEveryOwner     │ (Owns only) Enqueue all matching owners, not just the controller: true one. │                                                                                                                                     
  └─────────────────────────────┴─────────────────────────────────────────────────────────────────────────────┘                                                                                                                                     
                                                  
  ---                                                                                                                                                                                                                                               
  Builder-level config                            

  ┌───────────────────────────────────────┬───────────────────────────────────────────────────────────────────────────────────────────────────────────────────┐
  │                Method                 │                                                    Description                                                    │
  ├───────────────────────────────────────┼───────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤                                                                                     
  │ .WithEventFilter(p)                   │ Global predicate applied to all watches (except WatchesRawSource).                                                │
  ├───────────────────────────────────────┼───────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤                                                                                     
  │ .WithOptions(controller.Options{...}) │ MaxConcurrentReconciles, RateLimiter, RecoverPanic, NeedLeaderElection, ReconciliationTimeout, EnableWarmup, etc. │                                                                                     
  ├───────────────────────────────────────┼───────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤                                                                                     
  │ .Named(name)                          │ Sets controller name for metrics/logs.                                                                            │                                                                                     
  ├───────────────────────────────────────┼───────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤                                                                                     
  │ .WithLogConstructor(fn)               │ Per-reconcile logger factory.                                                                                     │
  └───────────────────────────────────────┴───────────────────────────────────────────────────────────────────────────────────────────────────────────────────┘                                                                                     
  
  ---                                                                                                                                                                                                                                               
  Handlers                                        

  ┌─────────────────────────────────────────────┬───────────────────────────────────────────────────────────────────────────────────────────────┐
  │                   Handler                   │                                          Description                                          │
  ├─────────────────────────────────────────────┼───────────────────────────────────────────────────────────────────────────────────────────────┤
  │ handler.EnqueueRequestForObject{}           │ Enqueue the event's own object (used implicitly by For).                                      │
  ├─────────────────────────────────────────────┼───────────────────────────────────────────────────────────────────────────────────────────────┤
  │ handler.EnqueueRequestForOwner(...)         │ Enqueue the owner. Takes handler.OnlyControllerOwner() option.                                │                                                                                                   
  ├─────────────────────────────────────────────┼───────────────────────────────────────────────────────────────────────────────────────────────┤
  │ handler.EnqueueRequestsFromMapFunc(fn)      │ Fan-out: func(ctx, obj) []reconcile.Request. What we use.                                     │                                                                                                   
  ├─────────────────────────────────────────────┼───────────────────────────────────────────────────────────────────────────────────────────────┤                                                                                                   
  │ handler.Funcs{CreateFunc, UpdateFunc, ...}  │ Ad-hoc per-event-type handler.                                                                │
  ├─────────────────────────────────────────────┼───────────────────────────────────────────────────────────────────────────────────────────────┤                                                                                                   
  │ handler.WithLowPriorityWhenUnchanged(inner) │ Wraps any handler to assign low priority on re-list/resync events. Requires UsePriorityQueue. │
  └─────────────────────────────────────────────┴───────────────────────────────────────────────────────────────────────────────────────────────┘                                                                                                   
  
  ---                                                                                                                                                                                                                                               
  Predicates                                      
            
  Concrete: GenerationChangedPredicate, ResourceVersionChangedPredicate, AnnotationChangedPredicate, LabelChangedPredicate, LabelSelectorPredicate
                                                                                                                                                                                                                                                    
  Combinators: predicate.And(...), predicate.Or(...), predicate.Not(...)
                                                                                                                                                                                                                                                    
  Custom: predicate.NewPredicateFuncs(func(obj) bool), predicate.Funcs{CreateFunc, UpdateFunc, ...} 


  The distinction from predicates/indexes:                                                                                                                                                                                                          
                                                  
  ┌──────────────────────────┬───────────────────────────────┬────────────────────────────────┬───────────────────────────┐                                                                                                                         
  │                          │        cache.ByObject         │           Predicates           │       Field indexes       │
  ├──────────────────────────┼───────────────────────────────┼────────────────────────────────┼───────────────────────────┤                                                                                                                         
  │ Where filtering happens  │ API server (watch filter)     │ In-process, after cache        │ In-process, at query time │
  ├──────────────────────────┼───────────────────────────────┼────────────────────────────────┼───────────────────────────┤
  │ What it affects          │ What's stored in cache at all │ Which events trigger reconcile │ How you query the cache   │                                                                                                                         
  ├──────────────────────────┼───────────────────────────────┼────────────────────────────────┼───────────────────────────┤                                                                                                                         
  │ Reduces memory?          │ Yes                           │ No                             │ No                        │                                                                                                                         
  ├──────────────────────────┼───────────────────────────────┼────────────────────────────────┼───────────────────────────┤                                                                                                                         
  │ Reduces API server load? │ Yes                           │ No                             │ No                        │
  └──────────────────────────┴───────────────────────────────┴────────────────────────────────┴───────────────────────────┘                                                                                                                         
                                                  
  You can filter by Label (label selector) and Namespaces (per-namespace overrides). Field selectors (Field) are theoretically supported but most resource types only support a few built-in fields server-side (e.g. spec.nodeName for Pods) —     
  custom CRD fields aren't indexable server-side.


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
or even better, maybe agentgateway already does this. Otherwise, we'd have to push changes from the mycelium controller.
Also, kmcp might implement sessions? or does AGW already implement mcp sessions?
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
We can tighten / stricted the predicate logic in each reconciler's watches to minimize
the number of reconciles (e.g. we probably only need to reconcile on project deletes)
Later on we'll also wanna add "Owns(...)" to credentialprovider_reconciler, and all the others. I think we're actually using it wrong rn, because the controller doesn't own

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

## Migrate to typed Apply API
- [ ] controller-runtime v0.23+ deprecates `Patch(ctx, obj, client.Apply, ...)` in favor of `Apply(ctx, applyConfig, ...)` which takes typed `runtime.ApplyConfiguration` objects instead of full `runtime.Object`s. This is more explicit — the apply config only contains fields you have opinions on, avoiding accidental field ownership. Requires changing the `generate` package to return apply configurations (e.g., `corev1apply.ServiceAccountApplyConfiguration`) instead of full objects. Not urgent since the old API still works, but should be done before the API is removed.

## Knative
- [ ] Verify Knative Serving compatibility with Istio ambient mesh (OQ-5)
- [ ] Verify `runtimeClassName: kata-fc` works with Knative pod templates
- [ ] Test scale-to-zero and cold start latency for tool executors
- [ ] Investigate Knative func templates as reference for tool developer SDK

