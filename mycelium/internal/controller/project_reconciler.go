package controller

import (
	"context"
	"fmt"
	"slices"

	agwv1alpha1 "github.com/agentgateway/agentgateway/controller/api/v1alpha1/agentgateway"
	v1alpha1 "mycelium.io/mycelium/api/v1alpha1"
	"mycelium.io/mycelium/internal/generate"
	"mycelium.io/mycelium/pkg/wellknown"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

const ProjectFinalizer = "mycelium.io/project-cleanup"

type ProjectReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=mycelium.io,resources=projects,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=mycelium.io,resources=projects/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=mycelium.io,resources=projects/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=agentgateway.dev,resources=agentgatewaybackends;agentgatewaypolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=get;list;watch;create;update;patch;delete

func (r *ProjectReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, retErr error) {
	logger := log.FromContext(ctx)

	var proj v1alpha1.Project
	if err := r.Get(ctx, req.NamespacedName, &proj); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	patchHelper, err := patch.NewHelper(&proj, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}

	defer func() {
		if err := patchHelper.Patch(ctx, &proj,
			patch.WithOwnedConditions{Conditions: []string{
				v1alpha1.ReadyCondition,
				v1alpha1.NamespaceCondition,
				v1alpha1.MCPBackendCondition,
				v1alpha1.MCPRouteCondition,
				v1alpha1.JWTPolicyCondition,
				v1alpha1.SourceContextPolicyCondition,
				v1alpha1.ToolAccessPolicyCondition,
			}},
			patch.WithStatusObservedGeneration{},
		); err != nil {
			retErr = kerrors.NewAggregate([]error{retErr, err})
		}
	}()

	if !proj.DeletionTimestamp.IsZero() {
		logger.Info("Cleaning up Project", "name", proj.Name)
		return r.reconcileDelete(ctx, &proj)
	}

	if controllerutil.AddFinalizer(&proj, ProjectFinalizer) {
		proj.Status.SetStatusCondition(metav1.Condition{
			Type:    v1alpha1.ReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha1.PendingReason,
			Message: "Pending",
		})
		return ctrl.Result{}, nil
	}

	proj.Status.SetStatusCondition(metav1.Condition{
		Type:    v1alpha1.ReadyCondition,
		Status:  metav1.ConditionFalse,
		Reason:  v1alpha1.ProvisioningReason,
		Message: "Provisioning",
	})

	logger.Info("Reconciling Project", "name", proj.Name)

	// Run all phases, collecting retryable errors and tracking sub-resource readiness.
	anyFailed, anyProgressing := false, false
	var errs []error
	for _, phase := range []func(context.Context, *v1alpha1.Project) (phaseStatus, error){
		r.reconcileNamespace,
		r.reconcileMCPBackend,
		r.reconcileMCPRoute,
		r.reconcileJWTPolicy,
		r.reconcileSourceContextPolicy,
		r.reconcileToolAccessPolicy,
	} {
		st, err := phase(ctx, &proj)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		switch st {
		case phaseFailed:
			anyFailed = true
		case phaseProgressing:
			anyProgressing = true
		}
	}

	if err := kerrors.NewAggregate(errs); err != nil {
		logger.Error(err, "Failed to reconcile project")
		return ctrl.Result{}, err
	}

	switch {
	case anyFailed:
		proj.Status.SetStatusCondition(metav1.Condition{
			Type:    v1alpha1.ReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha1.FailedReason,
			Message: "One or more sub-resources failed",
		})
	case anyProgressing:
		proj.Status.SetStatusCondition(metav1.Condition{
			Type:    v1alpha1.ReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha1.ProvisioningReason,
			Message: "Waiting for sub-resources to become ready",
		})
	default:
		proj.Status.SetStatusCondition(metav1.Condition{
			Type:    v1alpha1.ReadyCondition,
			Status:  metav1.ConditionTrue,
			Reason:  v1alpha1.RunningReason,
			Message: "Running",
		})
	}
	return ctrl.Result{}, nil
}

func (r *ProjectReconciler) ownerRef(proj *v1alpha1.Project) *metav1ac.OwnerReferenceApplyConfiguration {
	return metav1ac.OwnerReference().
		WithName(proj.Name).
		WithUID(proj.UID).
		WithKind(wellknown.ProjectGVK.Kind).
		WithAPIVersion(wellknown.ProjectGVK.GroupVersion().String()).
		WithController(true).
		WithBlockOwnerDeletion(true)
}

func (r *ProjectReconciler) reconcileNamespace(ctx context.Context, proj *v1alpha1.Project) (phaseStatus, error) {
	ns := generate.Namespace(proj).WithOwnerReferences(r.ownerRef(proj))
	if err := r.Apply(ctx, ns, client.FieldOwner(generate.ManagedBy), client.ForceOwnership); err != nil {
		if isTerminalAPIErr(err) {
			proj.Status.SetStatusCondition(metav1.Condition{
				Type:    v1alpha1.NamespaceCondition,
				Status:  metav1.ConditionFalse,
				Reason:  v1alpha1.InvalidReason,
				Message: err.Error(),
			})
			return phaseFailed, nil
		}
		return phaseProgressing, fmt.Errorf("applying namespace %s: %w", proj.Name, err)
	}
	proj.Status.Namespace = &corev1.TypedLocalObjectReference{
		Kind: *ns.GetKind(),
		Name: *ns.GetName(),
	}
	// Namespaces have no async controller — ready as soon as applied.
	proj.Status.SetStatusCondition(metav1.Condition{
		Type:    v1alpha1.NamespaceCondition,
		Status:  metav1.ConditionTrue,
		Reason:  v1alpha1.CreatedReason,
		Message: fmt.Sprintf("Namespace %s created", proj.Name),
	})
	return phaseDone, nil
}

func (r *ProjectReconciler) reconcileMCPBackend(ctx context.Context, proj *v1alpha1.Project) (phaseStatus, error) {
	mcpBackend := generate.MCPBackend(proj).WithOwnerReferences(r.ownerRef(proj))
	if err := r.Apply(ctx, mcpBackend, client.FieldOwner(generate.ManagedBy), client.ForceOwnership); err != nil {
		if isTerminalAPIErr(err) {
			proj.Status.SetStatusCondition(metav1.Condition{
				Type:    v1alpha1.MCPBackendCondition,
				Status:  metav1.ConditionFalse,
				Reason:  v1alpha1.InvalidReason,
				Message: err.Error(),
			})
			return phaseFailed, nil
		}
		return phaseProgressing, fmt.Errorf("applying MCPBackend: %w", err)
	}

	agwGroup := wellknown.AgentgatewayBackendGVK.Group
	proj.Status.MCPBackend = &corev1.TypedLocalObjectReference{
		APIGroup: &agwGroup,
		Kind:     *mcpBackend.GetKind(),
		Name:     *mcpBackend.GetName(),
	}

	var backend agwv1alpha1.AgentgatewayBackend
	if err := r.Get(ctx, types.NamespacedName{Name: *mcpBackend.GetName(), Namespace: proj.Name}, &backend); err != nil {
		if apierrors.IsNotFound(err) {
			proj.Status.SetStatusCondition(metav1.Condition{
				Type:    v1alpha1.MCPBackendCondition,
				Status:  metav1.ConditionFalse,
				Reason:  v1alpha1.ProgressingReason,
				Message: "Waiting for AgentgatewayBackend to be created",
			})
			return phaseProgressing, nil
		}
		return phaseProgressing, fmt.Errorf("getting MCPBackend: %w", err)
	}

	return agwBackendPhaseStatus(proj, &backend, v1alpha1.MCPBackendCondition, "AgentgatewayBackend"), nil
}

func (r *ProjectReconciler) reconcileMCPRoute(ctx context.Context, proj *v1alpha1.Project) (phaseStatus, error) {
	obj := generate.MCPRoute(proj).WithOwnerReferences(r.ownerRef(proj))
	if err := r.Apply(ctx, obj, client.FieldOwner(generate.ManagedBy), client.ForceOwnership); err != nil {
		if isTerminalAPIErr(err) {
			proj.Status.SetStatusCondition(metav1.Condition{
				Type:    v1alpha1.MCPRouteCondition,
				Status:  metav1.ConditionFalse,
				Reason:  v1alpha1.InvalidReason,
				Message: err.Error(),
			})
			return phaseFailed, nil
		}
		return phaseProgressing, fmt.Errorf("applying MCPRoute: %w", err)
	}

	httpGroup := wellknown.HTTPRouteGVK.Group
	proj.Status.MCPRoute = &corev1.TypedLocalObjectReference{
		APIGroup: &httpGroup,
		Kind:     *obj.GetKind(),
		Name:     *obj.GetName(),
	}

	var route gwv1.HTTPRoute
	if err := r.Get(ctx, types.NamespacedName{Name: *obj.GetName(), Namespace: proj.Name}, &route); err != nil {
		if apierrors.IsNotFound(err) {
			proj.Status.SetStatusCondition(metav1.Condition{
				Type:    v1alpha1.MCPRouteCondition,
				Status:  metav1.ConditionFalse,
				Reason:  v1alpha1.ProgressingReason,
				Message: "Waiting for HTTPRoute to be created",
			})
			return phaseProgressing, nil
		}
		return phaseProgressing, fmt.Errorf("getting MCPRoute: %w", err)
	}

	// Accepted=True on any parent means the gateway accepted the route.
	for _, parent := range route.Status.Parents {
		cond := findCondition(parent.Conditions, "Accepted")
		if cond != nil && cond.Status == metav1.ConditionTrue {
			proj.Status.SetStatusCondition(metav1.Condition{
				Type:   v1alpha1.MCPRouteCondition,
				Status: metav1.ConditionTrue,
				Reason: v1alpha1.SyncedReason,
			})
			return phaseDone, nil
		}
	}
	proj.Status.SetStatusCondition(metav1.Condition{
		Type:    v1alpha1.MCPRouteCondition,
		Status:  metav1.ConditionFalse,
		Reason:  v1alpha1.ProgressingReason,
		Message: "Waiting for HTTPRoute to be accepted by the gateway",
	})
	return phaseProgressing, nil
}

func (r *ProjectReconciler) reconcileJWTPolicy(ctx context.Context, proj *v1alpha1.Project) (phaseStatus, error) {
	obj := generate.JWTPolicy(proj).WithOwnerReferences(r.ownerRef(proj))
	if err := r.Apply(ctx, obj, client.FieldOwner(generate.ManagedBy), client.ForceOwnership); err != nil {
		if isTerminalAPIErr(err) {
			proj.Status.SetStatusCondition(metav1.Condition{
				Type:    v1alpha1.JWTPolicyCondition,
				Status:  metav1.ConditionFalse,
				Reason:  v1alpha1.InvalidReason,
				Message: err.Error(),
			})
			return phaseFailed, nil
		}
		return phaseProgressing, fmt.Errorf("applying JWTPolicy: %w", err)
	}

	agwGroup := wellknown.AgentgatewayPolicyGVK.Group
	proj.Status.JWTPolicy = &corev1.TypedLocalObjectReference{
		APIGroup: &agwGroup,
		Kind:     wellknown.AgentgatewayPolicyGVK.Kind,
		Name:     *obj.GetName(),
	}

	var policy agwv1alpha1.AgentgatewayPolicy
	if err := r.Get(ctx, types.NamespacedName{Name: *obj.GetName(), Namespace: proj.Name}, &policy); err != nil {
		if apierrors.IsNotFound(err) {
			proj.Status.SetStatusCondition(metav1.Condition{
				Type:    v1alpha1.JWTPolicyCondition,
				Status:  metav1.ConditionFalse,
				Reason:  v1alpha1.ProgressingReason,
				Message: "Waiting for AgentgatewayPolicy to be created",
			})
			return phaseProgressing, nil
		}
		return phaseProgressing, fmt.Errorf("getting JWTPolicy: %w", err)
	}
	return agwPolicyPhaseStatus(proj, &policy, v1alpha1.JWTPolicyCondition, "JWTPolicy"), nil
}

func (r *ProjectReconciler) reconcileSourceContextPolicy(ctx context.Context, proj *v1alpha1.Project) (phaseStatus, error) {
	obj := generate.SourceContextPolicy(proj).WithOwnerReferences(r.ownerRef(proj))
	if err := r.Apply(ctx, obj, client.FieldOwner(generate.ManagedBy), client.ForceOwnership); err != nil {
		if isTerminalAPIErr(err) {
			proj.Status.SetStatusCondition(metav1.Condition{
				Type:    v1alpha1.SourceContextPolicyCondition,
				Status:  metav1.ConditionFalse,
				Reason:  v1alpha1.InvalidReason,
				Message: err.Error(),
			})
			return phaseFailed, nil
		}
		return phaseProgressing, fmt.Errorf("applying SourceContextPolicy: %w", err)
	}

	agwGroup := wellknown.AgentgatewayPolicyGVK.Group
	proj.Status.SourceContextPolicy = &corev1.TypedLocalObjectReference{
		APIGroup: &agwGroup,
		Kind:     wellknown.AgentgatewayPolicyGVK.Kind,
		Name:     *obj.GetName(),
	}

	var policy agwv1alpha1.AgentgatewayPolicy
	if err := r.Get(ctx, types.NamespacedName{Name: *obj.GetName(), Namespace: proj.Name}, &policy); err != nil {
		if apierrors.IsNotFound(err) {
			proj.Status.SetStatusCondition(metav1.Condition{
				Type:    v1alpha1.SourceContextPolicyCondition,
				Status:  metav1.ConditionFalse,
				Reason:  v1alpha1.ProgressingReason,
				Message: "Waiting for AgentgatewayPolicy to be created",
			})
			return phaseProgressing, nil
		}
		return phaseProgressing, fmt.Errorf("getting SourceContextPolicy: %w", err)
	}
	return agwPolicyPhaseStatus(proj, &policy, v1alpha1.SourceContextPolicyCondition, "SourceContextPolicy"), nil
}

func (r *ProjectReconciler) reconcileToolAccessPolicy(ctx context.Context, proj *v1alpha1.Project) (phaseStatus, error) {
	var agents v1alpha1.AgentList
	if err := r.List(ctx, &agents, client.InNamespace(proj.Name)); err != nil {
		return phaseProgressing, fmt.Errorf("listing agents: %w", err)
	}
	obj := generate.ToolAccessPolicy(proj, agents.Items).WithOwnerReferences(r.ownerRef(proj))
	if err := r.Apply(ctx, obj, client.FieldOwner(generate.ManagedBy), client.ForceOwnership); err != nil {
		if isTerminalAPIErr(err) {
			proj.Status.SetStatusCondition(metav1.Condition{
				Type:    v1alpha1.ToolAccessPolicyCondition,
				Status:  metav1.ConditionFalse,
				Reason:  v1alpha1.InvalidReason,
				Message: err.Error(),
			})
			return phaseFailed, nil
		}
		return phaseProgressing, fmt.Errorf("applying ToolAccessPolicy: %w", err)
	}

	agwGroup := wellknown.AgentgatewayPolicyGVK.Group
	proj.Status.ToolAccessPolicy = &corev1.TypedLocalObjectReference{
		APIGroup: &agwGroup,
		Kind:     *obj.GetKind(),
		Name:     *obj.GetName(),
	}

	var policy agwv1alpha1.AgentgatewayPolicy
	if err := r.Get(ctx, types.NamespacedName{Name: *obj.GetName(), Namespace: proj.Name}, &policy); err != nil {
		if apierrors.IsNotFound(err) {
			proj.Status.SetStatusCondition(metav1.Condition{
				Type:    v1alpha1.ToolAccessPolicyCondition,
				Status:  metav1.ConditionFalse,
				Reason:  v1alpha1.ProgressingReason,
				Message: "Waiting for AgentgatewayPolicy to be created",
			})
			return phaseProgressing, nil
		}
		return phaseProgressing, fmt.Errorf("getting ToolAccessPolicy: %w", err)
	}
	return agwPolicyPhaseStatus(proj, &policy, v1alpha1.ToolAccessPolicyCondition, "ToolAccessPolicy"), nil
}

// agwBackendPhaseStatus reads the Accepted condition from an AgentgatewayBackend and
// sets the corresponding project condition.
func agwBackendPhaseStatus(proj *v1alpha1.Project, backend *agwv1alpha1.AgentgatewayBackend, condType, resourceName string) phaseStatus {
	cond := findCondition(backend.Status.Conditions, "Accepted")
	if cond == nil {
		proj.Status.SetStatusCondition(metav1.Condition{
			Type:    condType,
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha1.ProgressingReason,
			Message: fmt.Sprintf("Waiting for %s to be accepted", resourceName),
		})
		return phaseProgressing
	}
	if cond.Status == metav1.ConditionTrue {
		proj.Status.SetStatusCondition(metav1.Condition{
			Type:   condType,
			Status: metav1.ConditionTrue,
			Reason: v1alpha1.SyncedReason,
		})
		return phaseDone
	}
	// Accepted=False: terminal if Invalid, otherwise still progressing.
	proj.Status.SetStatusCondition(metav1.Condition{
		Type:    condType,
		Status:  metav1.ConditionFalse,
		Reason:  cond.Reason,
		Message: cond.Message,
	})
	if cond.Reason == v1alpha1.InvalidReason {
		return phaseFailed
	}
	return phaseProgressing
}

// agwPolicyPhaseStatus reads the Accepted condition from an AgentgatewayPolicy's ancestors
// and sets the corresponding project condition.
func agwPolicyPhaseStatus(proj *v1alpha1.Project, policy *agwv1alpha1.AgentgatewayPolicy, condType, resourceName string) phaseStatus {
	for _, ancestor := range policy.Status.Ancestors {
		cond := findCondition(ancestor.Conditions, "Accepted")
		if cond == nil {
			continue
		}
		if cond.Status == metav1.ConditionTrue {
			proj.Status.SetStatusCondition(metav1.Condition{
				Type:   condType,
				Status: metav1.ConditionTrue,
				Reason: v1alpha1.SyncedReason,
			})
			return phaseDone
		}
		// Accepted=False: terminal if Invalid, otherwise still progressing.
		proj.Status.SetStatusCondition(metav1.Condition{
			Type:    condType,
			Status:  metav1.ConditionFalse,
			Reason:  cond.Reason,
			Message: cond.Message,
		})
		if cond.Reason == v1alpha1.InvalidReason {
			return phaseFailed
		}
		return phaseProgressing
	}
	// No ancestors with a condition yet — AGW controller hasn't reconciled.
	proj.Status.SetStatusCondition(metav1.Condition{
		Type:    condType,
		Status:  metav1.ConditionFalse,
		Reason:  v1alpha1.ProgressingReason,
		Message: fmt.Sprintf("Waiting for %s to be accepted", resourceName),
	})
	return phaseProgressing
}

func (r *ProjectReconciler) reconcileDelete(ctx context.Context, proj *v1alpha1.Project) (ctrl.Result, error) {
	var tools v1alpha1.ToolList
	if err := r.List(ctx, &tools, client.InNamespace(proj.Name)); err != nil {
		return ctrl.Result{}, err
	}
	var cps v1alpha1.CredentialProviderList
	if err := r.List(ctx, &cps, client.InNamespace(proj.Name)); err != nil {
		return ctrl.Result{}, err
	}
	var agents v1alpha1.AgentList
	if err := r.List(ctx, &agents, client.InNamespace(proj.Name)); err != nil {
		return ctrl.Result{}, err
	}
	if len(tools.Items)+len(cps.Items)+len(agents.Items) > 0 {
		log.FromContext(ctx).Info("Project still has dependents",
			"tools", len(tools.Items), "cps", len(cps.Items), "agents", len(agents.Items))
		proj.Status.SetStatusCondition(metav1.Condition{
			Type:    v1alpha1.ReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha1.TerminatingReason,
			Message: fmt.Sprintf("Waiting for %d tool(s), %d credential provider(s), %d agent(s) to be deleted", len(tools.Items), len(cps.Items), len(agents.Items)),
		})
		return ctrl.Result{}, nil
	}

	controllerutil.RemoveFinalizer(proj, ProjectFinalizer)
	return ctrl.Result{}, nil
}

func (r *ProjectReconciler) mapToProject(_ context.Context, obj client.Object) []reconcile.Request {
	return []reconcile.Request{{
		NamespacedName: types.NamespacedName{Name: obj.GetNamespace()},
	}}
}

func agentToolListChanged() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			agent, ok := e.Object.(*v1alpha1.Agent)
			return !ok || len(agent.Spec.ToolBindings) > 0
		},
		DeleteFunc: func(event.DeleteEvent) bool { return true },
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldAgent, ok := e.ObjectOld.(*v1alpha1.Agent)
			if !ok {
				return true
			}
			newAgent, ok := e.ObjectNew.(*v1alpha1.Agent)
			if !ok {
				return true
			}
			return !slices.Equal(oldAgent.Spec.ToolBindings, newAgent.Spec.ToolBindings)
		},
	}
}

// isTerminalAPIErr reports whether an API error is non-transient.
// Non-transient errors should be surfaced as status conditions rather than
// requeued, since they won't resolve without an external change (CRD install,
// RBAC fix, or schema correction). IsInvalid covers 422 schema validation
// failures; IsForbidden covers admission webhook denials.
func isTerminalAPIErr(err error) bool {
	return apierrors.IsNotFound(err) ||
		apierrors.IsInvalid(err) ||
		apierrors.IsForbidden(err)
}

func (r *ProjectReconciler) SetupWithManager(mgr ctrl.Manager) error {
	deleteOnly := predicate.Funcs{DeleteFunc: func(event.DeleteEvent) bool { return true }}
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Project{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Owns(&corev1.Namespace{}).
		Owns(&agwv1alpha1.AgentgatewayBackend{}).
		Owns(&agwv1alpha1.AgentgatewayPolicy{}).
		Owns(&gwv1.HTTPRoute{}).
		Watches(&v1alpha1.Tool{}, handler.EnqueueRequestsFromMapFunc(r.mapToProject),
			builder.WithPredicates(deleteOnly)).
		Watches(&v1alpha1.CredentialProvider{}, handler.EnqueueRequestsFromMapFunc(r.mapToProject),
			builder.WithPredicates(deleteOnly)).
		Watches(&v1alpha1.Agent{}, handler.EnqueueRequestsFromMapFunc(r.mapToProject),
			builder.WithPredicates(agentToolListChanged())).
		Complete(r)
}
