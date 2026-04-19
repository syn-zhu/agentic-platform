package controller

import (
	"context"
	"fmt"

	"gorm.io/gorm/logger"
	v1alpha1 "mycelium.io/mycelium/api/v1alpha1"
	"mycelium.io/mycelium/internal/generate"

	agwv1alpha1 "github.com/agentgateway/agentgateway/controller/api/v1alpha1/agentgateway"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

const EcosystemFinalizer = "mycelium.io/ecosystem-cleanup"

// projectSubConditions lists all sub-resource condition types owned by the
// ProjectReconciler. The order here is the implicit priority order used by
// NewSummaryCondition when composing the root Ready condition.
var projectSubConditions = conditions.ForConditionTypes{
	v1alpha1.EcosystemNamespaceReadyCondition,
	v1alpha1.EcosystemGatewayReadyCondition,
	v1alpha1.EcosystemToolServerReadyCondition,
	v1alpha1.EcosystemToolServerRouteReadyCondition,
	v1alpha1.EcosystemAuthenticationPolicyReadyCondition,
	v1alpha1.SourceContextPolicyCondition,
	v1alpha1.ToolAccessPolicyCondition,
}

// EcosystemReconciler reconciles Project resources. It provisions the project
// namespace and all AGW resources (MCPBackend, MCPRoute, JWT policy, etc.).
type EcosystemReconciler struct {
	*Base
}

// +kubebuilder:rbac:groups=mycelium.io,resources=projects,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=mycelium.io,resources=projects/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=mycelium.io,resources=projects/finalizers,verbs=update
// +kubebuilder:rbac:groups=mycelium.io,resources=identityproviders,verbs=get;list;watch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update;patch;delete

func (r *EcosystemReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, retErr error) {
	logger := ctrl.LoggerFrom(ctx)

	var ecosystem v1alpha1.MyceliumEcosystem
	if err := r.Get(ctx, req.NamespacedName, &ecosystem); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	patchHelper, err := patch.NewHelper(&ecosystem, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}

	ownedConditions := append(projectSubConditions, v1alpha1.EcosystemReadyCondition)
	defer func() {
		if err := patchHelper.Patch(ctx, &ecosystem,
			patch.WithOwnedConditions{Conditions: ownedConditions},
			patch.WithStatusObservedGeneration{},
		); err != nil {
			retErr = errors.NewAggregate([]error{retErr, err})
		}
	}()

	if !ecosystem.DeletionTimestamp.IsZero() {
		logger.Info("Cleaning up Ecosystem")
		return r.reconcileDelete(ctx, &ecosystem)
	}

	if !controllerutil.ContainsFinalizer(&ecosystem, EcosystemFinalizer) {
		logger.Info("Initializing Ecosystem")
		return r.reconcileCreate(ctx, &ecosystem)
	}

	logger.Info("Reconciling Ecosystem")

	return r.reconcile(ctx, &ecosystem)
}

func (r *EcosystemReconciler) reconcileCreate(ctx context.Context, ecosystem *v1alpha1.MyceliumEcosystem) (ctrl.Result, error) {
	controllerutil.AddFinalizer(ecosystem, EcosystemFinalizer)
	conditions.Set(ecosystem, metav1.Condition{
		Type:   v1alpha1.EcosystemReadyCondition,
		Status: metav1.ConditionFalse,
		Reason: v1alpha1.EcosystemReadyInitializingReason,
	})
	return ctrl.Result{}, nil
}

var namespaceStatusChancedPredicate = predicate.Funcs{
	// TODO: actually maybe we should move
	// this to a separate reconciler, then it can handle namespace status changes independently
	// in that case we'd maybe wanna return true for create and delete
	CreateFunc: func(e event.CreateEvent) bool {
		return false
	},
	DeleteFunc: func(e event.DeleteEvent) bool {
		return false
	},
	UpdateFunc: func(e event.UpdateEvent) bool {
		if e.ObjectOld.GetResourceVersion() == e.ObjectNew.GetResourceVersion() {
			return false
		}
		oldNamespace, ok1 := e.ObjectOld.(*corev1.Namespace)
		newNamespace, ok2 := e.ObjectNew.(*corev1.Namespace)
		if !ok1 || !ok2 {
			return false
		}

		return !equality.Semantic.DeepEqual(oldNamespace.Status.Phase, newNamespace.Status.Phase)
	},
	GenericFunc: func(e event.GenericEvent) bool {
		return false
	},
}

func (r *EcosystemReconciler) reconcileNamespace(ctx context.Context, ecosystem *v1alpha1.MyceliumEcosystem) (ctrl.Result, error) {
	ns := generate.Namespace(ecosystem)
	// apply updates ns in-place with server state (including Status.Phase).
	objRef, err := r.apply(ctx, ecosystem, ns)
	if err != nil {

		return nil, fmt.Errorf("applying namespace %s: %w", ecosystem.Name, err)
	}
	ecosystem.Status.Namespace = objRef

	switch ns.Status.Phase {
	case corev1.NamespaceActive:
		return &metav1.Condition{
			Type:    v1alpha1.EcosystemNamespaceReadyCondition,
			Status:  metav1.ConditionTrue,
			Reason:  v1alpha1.ProvisionedReason,
			Message: fmt.Sprintf("Namespace %s is active", ns.Name),
		}, nil
	case corev1.NamespaceTerminating:
		return &metav1.Condition{
			Type:    v1alpha1.EcosystemNamespaceReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha1.DeletingReason,
			Message: fmt.Sprintf("Namespace %s is terminating; cannot provision resources inside it", ns.Name),
		}, nil
	default:
		return &metav1.Condition{
			Type:    v1alpha1.EcosystemNamespaceReadyCondition,
			Status:  metav1.ConditionUnknown,
			Reason:  v1alpha1.PendingReason,
			Message: fmt.Sprintf("Namespace %s phase is %q, waiting for Active", ns.Name, ns.Status.Phase),
		}, nil
	}

}

func (r *EcosystemReconciler) reconcile(ctx context.Context, proj *v1alpha1.MyceliumEcosystem) (ctrl.Result, error) {
	defer func() {
		// Use a custom priority function so that a True condition from a prior
		// generation is treated as Unknown: it hasn't been re-evaluated yet and
		// must not let the summary claim Ready=True prematurely.
		priorityFn := conditions.GetPriorityFunc(func(cond metav1.Condition) conditions.MergePriority {
			// TODO: why only true?
			if cond.Status == metav1.ConditionTrue && cond.ObservedGeneration != proj.Generation {
				return conditions.UnknownMergePriority
			}
			return conditions.GetDefaultMergePriorityFunc()(cond)
		})
		if err := conditions.SetSummaryCondition(proj, proj, v1alpha1.EcosystemReadyCondition,
			projectSubConditions,
			conditions.CustomMergeStrategy{MergeStrategy: conditions.DefaultMergeStrategy(priorityFn)},
		); err != nil {
			logger.Error(err, "setting summary condition for Project")
			conditions.Set(metav1.Condition{
				Type:    v1alpha1.EcosystemReadyCondition,
				Status:  metav1.ConditionUnknown,
				Reason:  v1alpha1.ErrorReason,
				Message: "Please check controller logs for errors",
			})
		}
	}()

	return ctrl.Result{}, r.run(proj)(ctx)
}

// reconcile drives the project provisioning DAG as a dependency event loop.
// Each task runs once all its required conditions are True; the topology is:
//
//	Namespace → TenantGateway → { MCPBackend → MCPRoute, JWTPolicy, SourceContextPolicy, ToolAccessPolicy }
func (r *EcosystemReconciler) run(proj *v1alpha1.MyceliumEcosystem) func(context.Context) error {
	return runTasks(proj, r)(
		do(stageNamespace),
		do(stageTenantGateway, v1alpha1.EcosystemNamespaceReadyCondition),
		do(stageMCPBackend, v1alpha1.EcosystemGatewayReadyCondition),
		do(stageMCPRoute, v1alpha1.EcosystemToolServerReadyCondition, v1alpha1.EcosystemGatewayReadyCondition),
		do(stageJWTPolicy, v1alpha1.EcosystemGatewayReadyCondition),
		do(stageSourceContextPolicy, v1alpha1.EcosystemGatewayReadyCondition),
		do(stageToolAccessPolicy, v1alpha1.EcosystemToolServerReadyCondition, v1alpha1.EcosystemGatewayReadyCondition),
	)
}

func stageTenantGateway(h helper, proj *v1alpha1.MyceliumEcosystem) stage {
	return func(ctx context.Context) error {
		gw := generate.Gateway(proj, "tenant-gateway")
		// apply updates gw in-place with server state (including Status.Conditions).
		objRef, err := h.apply(ctx, proj, gw)
		if err != nil {
			return fmt.Errorf("applying TenantGateway: %w", err)
		}
		proj.Status.Gateway = objRef

		accepted := meta.FindStatusCondition(gw.Status.Conditions, string(gwv1.GatewayConditionAccepted))
		if accepted != nil {
			if accepted.Status == metav1.ConditionFalse && accepted.Reason != string(gwv1.GatewayReasonNotReconciled) {
				proj.SetCondition(metav1.Condition{
					Type:    v1alpha1.EcosystemGatewayReadyCondition,
					Status:  metav1.ConditionFalse,
					Reason:  v1alpha1.NotReadyReason,
					Message: accepted.Message,
				})
				return nil
			}
			if accepted.Status == metav1.ConditionTrue && accepted.Reason != string(gwv1.GatewayReasonListenersNotValid) {
				programmed := meta.FindStatusCondition(gw.Status.Conditions, string(gwv1.GatewayConditionProgrammed))
				if programmed != nil {
					if programmed.Status == metav1.ConditionTrue {
						proj.SetCondition(metav1.Condition{
							Type:   v1alpha1.EcosystemGatewayReadyCondition,
							Status: metav1.ConditionTrue,
							Reason: v1alpha1.ProvisionedReason,
						})
						return nil
					}
					if programmed.Status == metav1.ConditionFalse && programmed.Reason != string(gwv1.GatewayReasonPending) {
						proj.SetCondition(metav1.Condition{
							Type:    v1alpha1.EcosystemGatewayReadyCondition,
							Status:  metav1.ConditionFalse,
							Reason:  v1alpha1.NotReadyReason,
							Message: programmed.Message,
						})
						return nil
					}
				}
			}
		}

		proj.SetCondition(metav1.Condition{
			Type:   v1alpha1.EcosystemGatewayReadyCondition,
			Status: metav1.ConditionUnknown,
			Reason: v1alpha1.PendingReason,
		})
		return nil
	}
}

func stageMCPBackend(h helper, proj *v1alpha1.MyceliumEcosystem) stage {
	return func(ctx context.Context) error {
		objRef, err := h.apply(ctx, proj, generate.ToolServer(proj, "mycelium-engine"))
		if err != nil {
			return fmt.Errorf("applying MCPBackend: %w", err)
		}
		proj.Status.MCPBackend = objRef
		proj.SetCondition(metav1.Condition{
			Type:   v1alpha1.EcosystemToolServerReadyCondition,
			Status: metav1.ConditionTrue,
			Reason: v1alpha1.ProvisionedReason,
		})
		return nil
	}
}

func stageMCPRoute(h helper, proj *v1alpha1.MyceliumEcosystem) stage {
	return func(ctx context.Context) error {
		objRef, err := h.apply(ctx, proj, generate.ToolServerRoute(proj, "mcp-route"))
		if err != nil {
			return fmt.Errorf("applying MCPRoute: %w", err)
		}
		proj.Status.MCPRoute = objRef
		proj.SetCondition(metav1.Condition{
			Type:   v1alpha1.EcosystemToolServerRouteReadyCondition,
			Status: metav1.ConditionTrue,
			Reason: v1alpha1.ProvisionedReason,
		})
		return nil
	}
}

func stageJWTPolicy(h helper, proj *v1alpha1.MyceliumEcosystem) stage {
	return func(ctx context.Context) error {
		var idps v1alpha1.IdentityProviderList
		if err := h.List(ctx, &idps, client.InNamespace(proj.Name)); err != nil {
			return fmt.Errorf("listing IdentityProviders: %w", err)
		}

		objRef, err := h.apply(ctx, proj, generate.JWTPolicy(proj, idps.Items))
		if err != nil {
			return fmt.Errorf("applying JWTPolicy: %w", err)
		}
		proj.Status.JWTPolicy = objRef

		proj.SetCondition(metav1.Condition{
			Type:   v1alpha1.EcosystemAuthenticationPolicyReadyCondition,
			Status: metav1.ConditionTrue,
			Reason: v1alpha1.ProvisionedReason,
		})
		return nil
	}
}

func stageSourceContextPolicy(h helper, proj *v1alpha1.MyceliumEcosystem) stage {
	return func(ctx context.Context) error {
		objRef, err := h.apply(ctx, proj, generate.SourceContextPolicy(proj))
		if err != nil {
			return fmt.Errorf("applying SourceContextPolicy: %w", err)
		}
		proj.Status.SourceContextPolicy = objRef
		proj.SetCondition(metav1.Condition{
			Type:   v1alpha1.SourceContextPolicyCondition,
			Status: metav1.ConditionTrue,
			Reason: v1alpha1.ProvisionedReason,
		})
		return nil
	}
}

func stageToolAccessPolicy(h helper, proj *v1alpha1.MyceliumEcosystem) stage {
	return func(ctx context.Context) error {
		var agents v1alpha1.MyceliumAgentList
		if err := h.List(ctx, &agents, client.InNamespace(proj.Name)); err != nil {
			return fmt.Errorf("listing Agents: %w", err)
		}

		objRef, err := h.apply(ctx, proj, generate.ToolAccessPolicy(proj, agents.Items))
		if err != nil {
			return fmt.Errorf("applying ToolAccessPolicy: %w", err)
		}
		proj.Status.ToolAccessPolicy = objRef
		proj.SetCondition(metav1.Condition{
			Type:   v1alpha1.ToolAccessPolicyCondition,
			Status: metav1.ConditionTrue,
			Reason: v1alpha1.ProvisionedReason,
		})
		return nil
	}
}

func (r *EcosystemReconciler) reconcileDelete(ctx context.Context, proj *v1alpha1.MyceliumEcosystem) (ctrl.Result, error) {
	logger := ctrl.LoggerFrom(ctx)
	// TODO: exclude deleting ones
	var tools v1alpha1.MyceliumToolList
	if err := r.List(ctx, &tools, client.InNamespace(proj.Name)); err != nil {
		return ctrl.Result{}, fmt.Errorf("listing Tools: %w", err)
	}

	var credentialProviders v1alpha1.MyceliumCredentialProviderList
	if err := r.List(ctx, &credentialProviders, client.InNamespace(proj.Name)); err != nil {
		return ctrl.Result{}, fmt.Errorf("listing CredentialProviders: %w", err)
	}

	var agents v1alpha1.MyceliumAgentList
	if err := r.List(ctx, &agents, client.InNamespace(proj.Name)); err != nil {
		return ctrl.Result{}, fmt.Errorf("listing Agents: %w", err)
	}

	var identityProviders v1alpha1.IdentityProviderList
	if err := r.List(ctx, &identityProviders, client.InNamespace(proj.Name)); err != nil {
		return ctrl.Result{}, fmt.Errorf("listing IdentityProviders: %w", err)
	}

	total := len(tools.Items) + len(credentialProviders.Items) + len(agents.Items) + len(identityProviders.Items)
	if total > 0 {
		logger.Info("Project still has dependents",
			"tools", len(tools.Items), "credentialProviders", len(credentialProviders.Items),
			"agents", len(agents.Items), "identityProviders", len(identityProviders.Items))
		conditions.Set(proj, metav1.Condition{
			Type:   v1alpha1.EcosystemReadyCondition,
			Status: metav1.ConditionFalse,
			Reason: v1alpha1.DeletingReason,
			Message: fmt.Sprintf(
				"Waiting for %d tool(s), %d credential provider(s), %d agent(s), %d identity provider(s) to be deleted",
				len(tools.Items), len(credentialProviders.Items), len(agents.Items), len(identityProviders.Items),
			),
		})
		return ctrl.Result{}, nil
	}

	controllerutil.RemoveFinalizer(proj, ProjectFinalizer)
	return ctrl.Result{}, nil
}

// specChanged fires on spec changes (generation bump), creates, and deletes.
func specChanged() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc:  func(event.CreateEvent) bool { return true },
		DeleteFunc:  func(event.DeleteEvent) bool { return true },
		GenericFunc: func(event.GenericEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			return e.ObjectNew.GetGeneration() != e.ObjectOld.GetGeneration()
		},
	}
}

func (r *EcosystemReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// deleteOnly: used for Tool/CP watches — only deletion matters for the finalizer.
	deleteOnly := predicate.Funcs{DeleteFunc: func(event.DeleteEvent) bool { return true }}
	// do(stageTenantGateway, v1alpha1.ProjectNamespaceReadyCondition),
	// 		do(stageMCPBackend, v1alpha1.ProjectTenantGatewayReadyCondition),
	// 		do(stageMCPRoute, v1alpha1.ProjectMCPBackendReadyCondition, v1alpha1.ProjectTenantGatewayReadyCondition),
	// 		do(stageJWTPolicy, v1alpha1.ProjectTenantGatewayReadyCondition),
	// 		do(stageSourceContextPolicy, v1alpha1.ProjectTenantGatewayReadyCondition),
	// 		do(stageToolAccessPolicy,
	return ctrl.NewControllerManagedBy(mgr).
		// TODO:
		Named("ecosystem-controller").
		For(&v1alpha1.MyceliumEcosystem{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Owns(&corev1.Namespace{}, builder.WithPredicates()).
		Owns(&gwv1.Gateway{}, builder.WithPredicates(predicate.Funcs{
			UpdateFunc: func(e event.UpdateEvent) bool {
				// Resync: apiserver didn't send anything new, informer just re-delivered.
				if e.ObjectOld.GetResourceVersion() == e.ObjectNew.GetResourceVersion() {
					return false
				}
				oldGateway, ok1 := e.ObjectOld.(*gwv1.Gateway)
				newGateway, ok2 := e.ObjectNew.(*gwv1.Gateway)
				if !ok1 || !ok2 {
					return false
				}
				// return !equality.Semantic.DeepEqual(oldGateway.Status, newGateway.Status)
				// TODO: check the status of the listeners (Name SectionName)
				// also check the gateway itself?
				// Figure out what we actually care about basically
			},
			CreateFunc: func(tce event.TypedCreateEvent[client.Object]) bool {
				return false
			},
			DeleteFunc: func(tde event.TypedDeleteEvent[client.Object]) bool {
				return false
			},
			GenericFunc: func(tge event.TypedGenericEvent[client.Object]) bool {
				return false
			},
		})).
		Owns(&agwv1alpha1.AgentgatewayBackend{}).
		Owns(&gwv1.HTTPRoute{}).
		Owns(&agwv1alpha1.AgentgatewayPolicy{}).

		// Agent changes affect ToolAccessPolicy.
		// TODO: Add predicate to filter on
		Watches(&v1alpha1.MyceliumAgent{},
			handler.EnqueueRequestsFromMapFunc(mapToEcosystem)).
		// otherwise we should not check the agent tool binding, just use the spec
		// Tool and CP: only deletion matters (finalizer gate).
		Watches(&v1alpha1.MyceliumTool{},
			handler.EnqueueRequestsFromMapFunc(mapToEcosystem),
			builder.WithPredicates(deleteOnly)).
		Watches(&v1alpha1.MyceliumCredentialProvider{},
			handler.EnqueueRequestsFromMapFunc(mapToEcosystem),
			builder.WithPredicates(deleteOnly)).
		Complete(r)
}

func mapToEcosystem(_ context.Context, obj client.Object) []reconcile.Request {
	return []reconcile.Request{{
		NamespacedName: types.NamespacedName{Name: obj.GetNamespace()},
	}}
}
