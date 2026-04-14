  ---
  Mycelium Controller Review — Consolidated Summary

  Critical (7 issues — must fix)

  ┌─────┬────────────┬───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┬───────────────────────────────────────────────────────────────────────────────┐
  │  #  │    Area    │                                                               Issue                                                               │                                     Files                                     │
  ├─────┼────────────┼───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┼───────────────────────────────────────────────────────────────────────────────┤
  │ C1  │ API Types  │ +kubebuilder:validation:ExactlyOneOf is not a real kubebuilder marker — silently ignored, oneOf constraint unenforced             │ credentialprovider_types.go:10,89, tool_types.go:30                           │
  ├─────┼────────────┼───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┼───────────────────────────────────────────────────────────────────────────────┤
  │ C2  │ Webhook    │ validateSecret nil-pointer panic when both oauth and apiKey are nil (direct consequence of C1)                                    │ credentialprovider_webhook.go:62-65, credentialprovider_reconciler.go:114-135 │
  ├─────┼────────────┼───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┼───────────────────────────────────────────────────────────────────────────────┤
  │ C3  │ Cmd        │ Webhooks are implemented but never registered with the manager — all admission validation bypassed at runtime                     │ cmd/controller/main.go                                                        │
  ├─────┼────────────┼───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┼───────────────────────────────────────────────────────────────────────────────┤
  │ C4  │ Controller │ Finalizer added via r.Update() but no early return — stale in-memory object causes Conflict errors on subsequent status writes    │ All 4 reconcilers (finalizer blocks)                                          │
  ├─────┼────────────┼───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┼───────────────────────────────────────────────────────────────────────────────┤
  │ C5  │ Controller │ ProjectReconciler does NOT watch Tool events — adding/deleting a Tool doesn't trigger policy regeneration; stale ToolAccessPolicy │ project_reconciler.go:224-229                                                 │
  ├─────┼────────────┼───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┼───────────────────────────────────────────────────────────────────────────────┤
  │ C6  │ Cmd        │ No health/readiness probes configured — Kubernetes has no liveness signal for the controller pod                                  │ cmd/controller/main.go:38-42                                                  │
  ├─────┼────────────┼───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┼───────────────────────────────────────────────────────────────────────────────┤
  │ C7  │ Cmd        │ metricsAddr flag parsed but never passed to ctrl.Options — flag is a no-op                                                        │ cmd/controller/main.go:33,38-42                                               │
  └─────┴────────────┴───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┴───────────────────────────────────────────────────────────────────────────────┘

  High (12 issues — should fix)

  ┌─────┬────────────┬─────────────────────────────────────────────────────────────────────────────────────────────────────────────┬───────────────────────────────────────────────┐
  │  #  │    Area    │                                                    Issue                                                    │                     Files                     │
  ├─────┼────────────┼─────────────────────────────────────────────────────────────────────────────────────────────────────────────┼───────────────────────────────────────────────┤
  │ H1  │ API Types  │ ObservedGeneration missing from all 4 status structs                                                        │ All *_types.go status structs                 │
  ├─────┼────────────┼─────────────────────────────────────────────────────────────────────────────────────────────────────────────┼───────────────────────────────────────────────┤
  │ H2  │ API Types  │ +kubebuilder:storageversion missing from all 4 CRDs                                                         │ All *_types.go root markers                   │
  ├─────┼────────────┼─────────────────────────────────────────────────────────────────────────────────────────────────────────────┼───────────────────────────────────────────────┤
  │ H3  │ API Types  │ Format=uri is advisory only — kube-apiserver does not enforce it; security-sensitive URLs accept any string │ project_types.go, credentialprovider_types.go │
  ├─────┼────────────┼─────────────────────────────────────────────────────────────────────────────────────────────────────────────┼───────────────────────────────────────────────┤
  │ H4  │ API Types  │ ShutdownTimeout regex rejects valid compound Go durations like "1h30m"                                      │ agent_types.go:40                             │
  ├─────┼────────────┼─────────────────────────────────────────────────────────────────────────────────────────────────────────────┼───────────────────────────────────────────────┤
  │ H5  │ API Types  │ WarmPoolRef references non-existent SandboxWarmPool CRD — dead forward-looking field                        │ agent_types.go:77-78                          │
  ├─────┼────────────┼─────────────────────────────────────────────────────────────────────────────────────────────────────────────┼───────────────────────────────────────────────┤
  │ H6  │ Controller │ All 4 reconcilers silently discard status update errors with _ = r.Status().Update()                        │ Multiple files                                │
  ├─────┼────────────┼─────────────────────────────────────────────────────────────────────────────────────────────────────────────┼───────────────────────────────────────────────┤
  │ H7  │ Controller │ Knative Service container has no SecurityContext — fails Restricted PSA                                     │ knative.go:47-57                              │
  ├─────┼────────────┼─────────────────────────────────────────────────────────────────────────────────────────────────────────────┼───────────────────────────────────────────────┤
  │ H8  │ Controller │ ProjectReconciler doesn't .Owns(&corev1.Namespace{}) — stale NamespaceReady condition on external deletion  │ project_reconciler.go:224-229                 │
  ├─────┼────────────┼─────────────────────────────────────────────────────────────────────────────────────────────────────────────┼───────────────────────────────────────────────┤
  │ H9  │ Config     │ RBAC role missing secrets permissions — CredentialProviderReconciler will always fail secret validation     │ config/rbac/role.yaml                         │
  ├─────┼────────────┼─────────────────────────────────────────────────────────────────────────────────────────────────────────────┼───────────────────────────────────────────────┤
  │ H10 │ Config     │ RBAC role missing coordination.k8s.io/leases — leader election will fail at runtime                         │ config/rbac/role.yaml                         │
  ├─────┼────────────┼─────────────────────────────────────────────────────────────────────────────────────────────────────────────┼───────────────────────────────────────────────┤
  │ H11 │ Config     │ Deployment, ServiceAccount, ClusterRoleBinding, and webhook infrastructure manifests entirely absent        │ config/                                       │
  ├─────┼────────────┼─────────────────────────────────────────────────────────────────────────────────────────────────────────────┼───────────────────────────────────────────────┤
  │ H12 │ Cmd        │ Logger hardcoded to dev mode (UseDevMode(true)) — non-JSON logs in production                               │ cmd/controller/main.go:36                     │
  └─────┴────────────┴─────────────────────────────────────────────────────────────────────────────────────────────────────────────┴───────────────────────────────────────────────┘

  Medium (10 issues — should address)

  ┌─────┬────────────┬───────────────────────────────────────────────────────────────────────────────────────────────┬──────────────────────────────────────────────────────┐
  │  #  │    Area    │                                             Issue                                             │                        Files                         │
  ├─────┼────────────┼───────────────────────────────────────────────────────────────────────────────────────────────┼──────────────────────────────────────────────────────┤
  │ M1  │ API Types  │ No oldSelf CEL immutability rules on security-critical fields (Issuer, ClientID)              │ All *_types.go                                       │
  ├─────┼────────────┼───────────────────────────────────────────────────────────────────────────────────────────────┼──────────────────────────────────────────────────────┤
  │ M2  │ API Types  │ No string constants for condition type names — typos silently create bad conditions           │ All *_types.go                                       │
  ├─────┼────────────┼───────────────────────────────────────────────────────────────────────────────────────────────┼──────────────────────────────────────────────────────┤
  │ M3  │ Controller │ No EventRecorder on any reconciler — no kubectl describe events for lifecycle transitions     │ All reconcilers                                      │
  ├─────┼────────────┼───────────────────────────────────────────────────────────────────────────────────────────────┼──────────────────────────────────────────────────────┤
  │ M4  │ Controller │ No per-reconcile context.WithTimeout — hung API calls block worker goroutines indefinitely    │ All reconcilers                                      │
  ├─────┼────────────┼───────────────────────────────────────────────────────────────────────────────────────────────┼──────────────────────────────────────────────────────┤
  │ M5  │ Controller │ MaxConcurrentReconciles not set explicitly on any controller                                  │ All SetupWithManager                                 │
  ├─────┼────────────┼───────────────────────────────────────────────────────────────────────────────────────────────┼──────────────────────────────────────────────────────┤
  │ M6  │ Controller │ CredentialProviderReconciler doesn't watch referenced Secrets — stale SecretValid on deletion │ credentialprovider_reconciler.go:219-225             │
  ├─────┼────────────┼───────────────────────────────────────────────────────────────────────────────────────────────┼──────────────────────────────────────────────────────┤
  │ M7  │ Controller │ ProjectReconciler has no .Owns() on AGW resources — external drift not self-healed            │ project_reconciler.go:226                            │
  ├─────┼────────────┼───────────────────────────────────────────────────────────────────────────────────────────────┼──────────────────────────────────────────────────────┤
  │ M8  │ Controller │ reconcileSecret called even when ProjectValid=False — may fail in terminating namespace       │ credentialprovider_reconciler.go:62-73               │
  ├─────┼────────────┼───────────────────────────────────────────────────────────────────────────────────────────────┼──────────────────────────────────────────────────────┤
  │ M9  │ Controller │ Webhook uses raw string index keys instead of constants from indexes.go — rename drift risk   │ tool_webhook.go:49, credentialprovider_webhook.go:48 │
  ├─────┼────────────┼───────────────────────────────────────────────────────────────────────────────────────────────┼──────────────────────────────────────────────────────┤
  │ M10 │ Controller │ Hardcoded resource names ("tenant-gateway", "mycelium-engine") not configurable               │ agw.go multiple lines                                │
  └─────┴────────────┴───────────────────────────────────────────────────────────────────────────────────────────────┴──────────────────────────────────────────────────────┘

  Top 5 Most Impactful — Fix These First

  1. C3 — Webhooks not registered: All your admission validation (deletion guards, ref checks) is dead code right now. Nothing prevents invalid objects from being created.
  2. C1+C2 — ExactlyOneOf is fake + nil panic: The "exactly one of" validation doesn't exist in kubebuilder. Combined with the missing nil guard, this is a runtime panic waiting to happen.
  3. C4 — Stale object after finalizer Update: Every reconcile that adds a finalizer will hit a Conflict error on the status update that follows. This causes unnecessary retries on every new resource.
  4. C5 — Missing Tool watch: Tool creates/deletes don't regenerate the access policy. Users will have stale routing until something else triggers a Project reconcile.
  5. H9+H10 — RBAC gaps: The controller will fail to read secrets and fail to acquire the leader lease. It won't start properly as-is.