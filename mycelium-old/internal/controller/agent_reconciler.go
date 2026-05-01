package controller

import (
	"context"
	"fmt"

	v1alpha1 "mycelium.io/mycelium/api/v1alpha1"
	"mycelium.io/mycelium/internal/generate"
	"mycelium.io/mycelium/internal/indexes"
	"mycelium.io/mycelium/pkg/wellknown"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const AgentFinalizer = "mycelium.io/agent-cleanup"

type AgentReconciler struct {
	*Base
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=mycelium.io,resources=agents,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=mycelium.io,resources=agents/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=mycelium.io,resources=agents/finalizers,verbs=update
// +kubebuilder:rbac:groups=mycelium.io,resources=tools,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete

func (r *AgentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, retErr error) {
	logger := log.FromContext(ctx)

	var agent v1alpha1.MyceliumAgent
	if err := r.Get(ctx, req.NamespacedName, &agent); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	patchHelper, err := patch.NewHelper(&agent, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}

	defer func() {
		if err := patchHelper.Patch(ctx, &agent,
			patch.WithOwnedConditions{Conditions: []string{v1alpha1.EcosystemReadyCondition, v1alpha1.ServiceAccountCondition}},
			patch.WithStatusObservedGeneration{},
		); err != nil {
			retErr = kerrors.NewAggregate([]error{retErr, err})
		}
	}()

	if !agent.DeletionTimestamp.IsZero() {
		logger.Info("Cleaning up Agent", "agent", agent.Name)
		return r.reconcileDelete(ctx, &agent)
	}

	logger.Info("Reconciling Agent", "agent", agent.Name)

	// Prerequisites: validate sequentially, fail early.
	if err := r.resolveProject(ctx, &agent); err != nil {
		return ctrl.Result{}, err
	}

	// Owned resources: reconcile with error aggregation.
	if err := r.reconcileServiceAccount(ctx, &agent); err != nil {
		return ctrl.Result{}, err
	}

	// Resolve tool bindings; only commit to status if no transient errors.
	tools, err := r.resolveToolBindings(ctx, &agent)
	if err != nil {
		return ctrl.Result{}, err
	}
	agent.Status.ToolBindings = tools

	return ctrl.Result{}, nil
}

// resolveProject checks that the parent Project exists.
func (r *AgentReconciler) resolveProject(ctx context.Context, agent *v1alpha1.MyceliumAgent) error {
	var proj v1alpha1.MyceliumEcosystem
	if err := r.Get(ctx, types.NamespacedName{Name: agent.Namespace}, &proj); err != nil {
		if errors.IsNotFound(err) {
			// TODO: set status
			return fmt.Errorf("Project %s not found", agent.Namespace)
		}
		return fmt.Errorf("checking Project: %w", err)
	}
	return nil
}

// reconcileServiceAccount ensures the per-agent ServiceAccount exists.
func (r *AgentReconciler) reconcileServiceAccount(ctx context.Context, agent *v1alpha1.MyceliumAgent) error {
	sa := generate.ServiceAccount(agent)
	if err := controllerutil.SetControllerReference(agent, sa, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on ServiceAccount: %w", err)
	}
	if err := r.Patch(ctx, sa, client.Apply, client.FieldOwner(wellknown.MyceliumControllerName), client.ForceOwnership); err != nil {
		return fmt.Errorf("applying ServiceAccount %s: %w", agent.Name, err)
	}

	saAPIGroup := ""
	agent.Status.ServiceAccount = &v1alpha1.ReferencedResourceStatus{
		ResourceRef: &corev1.TypedLocalObjectReference{
			APIGroup: &saAPIGroup,
			Kind:     "ServiceAccount",
			Name:     sa.Name,
		},
	}
	apimeta.SetStatusCondition(&agent.Status.Conditions, metav1.Condition{
		Type:    v1alpha1.ServiceAccountCondition,
		Status:  metav1.ConditionTrue,
		Reason:  v1alpha1.CreatedReason,
		Message: fmt.Sprintf("ServiceAccount %s created", agent.Name),
	})
	return nil
}

// resolveToolBindings resolves each ToolBinding and returns the computed
// ReferenceStatus slice without writing to agent.Status. Any API error for an
// individual tool is transient; the caller should not commit the result.
func (r *AgentReconciler) resolveToolBindings(ctx context.Context, agent *v1alpha1.MyceliumAgent) ([]v1alpha1.ReferencedResourceStatus, error) {
	toolAPIGroup := v1alpha1.GroupVersion.Group
	tools := make([]v1alpha1.ReferencedResourceStatus, 0, len(agent.Spec.ToolBindings))

	var errs []error
	for _, tb := range agent.Spec.ToolBindings {
		ref := v1alpha1.ReferencedResourceStatus{
			ResourceRef: &corev1.TypedLocalObjectReference{
				APIGroup: &toolAPIGroup,
				Kind:     "Tool",
				Name:     tb.Tool.Name,
			},
		}

		var tool v1alpha1.MyceliumTool
		if err := r.Get(ctx, types.NamespacedName{Name: tb.Tool.Name, Namespace: agent.Namespace}, &tool); err != nil {
			if errors.IsNotFound(err) {
				apimeta.SetStatusCondition(&ref.Conditions, metav1.Condition{
					Type:    v1alpha1.EcosystemReadyCondition,
					Status:  metav1.ConditionFalse,
					Reason:  v1alpha1.FailedReason,
					Message: fmt.Sprintf("Tool %s not found", tb.Tool.Name),
				})
			} else {
				errs = append(errs, fmt.Errorf("getting Tool %s: %w", tb.Tool.Name, err))
				continue
			}
		} else if apimeta.IsStatusConditionTrue(tool.Status.Conditions, v1alpha1.EcosystemReadyCondition) {
			apimeta.SetStatusCondition(&ref.Conditions, metav1.Condition{
				Type:   v1alpha1.EcosystemReadyCondition,
				Status: metav1.ConditionTrue,
				Reason: v1alpha1.ProvisionedReason,
			})
		} else {
			apimeta.SetStatusCondition(&ref.Conditions, metav1.Condition{
				Type:    v1alpha1.EcosystemReadyCondition,
				Status:  metav1.ConditionUnknown,
				Reason:  v1alpha1.ProgressingReason,
				Message: fmt.Sprintf("Tool %s is not yet ready", tb.Tool.Name),
			})
		}

		tools = append(tools, ref)
	}

	return tools, kerrors.NewAggregate(errs)
}

func (r *AgentReconciler) reconcileDelete(_ context.Context, agent *v1alpha1.MyceliumAgent) (ctrl.Result, error) {
	controllerutil.RemoveFinalizer(agent, AgentFinalizer)
	return ctrl.Result{}, nil
}

// mapToolToAgents maps a Tool to all Agents in the same namespace that bind it.
func (r *AgentReconciler) mapToolToAgents(ctx context.Context, obj client.Object) []reconcile.Request {
	var agents v1alpha1.MyceliumAgentList
	if err := r.List(ctx, &agents,
		client.InNamespace(obj.GetNamespace()),
		client.MatchingFields{indexes.IndexAgentToolBindings: obj.GetName()},
	); err != nil {
		return nil
	}
	requests := make([]reconcile.Request, 0, len(agents.Items))
	for _, a := range agents.Items {
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: a.Name, Namespace: a.Namespace},
		})
	}
	return requests
}

func (r *AgentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.MyceliumAgent{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Owns(&corev1.ServiceAccount{}).
		// Re-evaluate tool bindings when a referenced Tool changes.
		Watches(&v1alpha1.MyceliumTool{}, handler.EnqueueRequestsFromMapFunc(r.mapToolToAgents)).
		Complete(r)
}
