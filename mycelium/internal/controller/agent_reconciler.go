package controller

import (
	"context"
	"fmt"

	v1alpha1 "github.com/mongodb/mycelium/api/v1alpha1"
	"github.com/mongodb/mycelium/internal/generate"
	myceliumutil "github.com/mongodb/mycelium/internal/util"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const AgentFinalizer = "mycelium.io/agent-cleanup"

type AgentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=mycelium.io,resources=agents,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=mycelium.io,resources=agents/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=mycelium.io,resources=agents/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete

func (r *AgentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, reterr error) {
	logger := log.FromContext(ctx)

	var agent v1alpha1.Agent
	if err := r.Get(ctx, req.NamespacedName, &agent); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !agent.DeletionTimestamp.IsZero() {
		logger.Info("Cleaning up Agent", "agent", agent.Name)
		return r.reconcileDelete(ctx, &agent)
	}

	if !controllerutil.ContainsFinalizer(&agent, AgentFinalizer) {
		return r.reconcileCreate(ctx, &agent)
	}

	original := agent.DeepCopy()

	defer func() {
		if !equality.Semantic.DeepEqual(original.Status, agent.Status) {
			if err := r.Status().Patch(ctx, &agent, client.MergeFrom(original)); err != nil {
				reterr = kerrors.NewAggregate([]error{reterr, err})
			}
		}
	}()

	logger.Info("Reconciling Agent", "agent", agent.Name)

	var errs []error
	res := ctrl.Result{}
	for _, phase := range []func(context.Context, *v1alpha1.Agent) (ctrl.Result, error){
		r.reconcileServiceAccount,
	} {
		phaseResult, err := phase(ctx, &agent)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		res = myceliumutil.LowestNonZeroResult(res, phaseResult)
	}

	if len(errs) > 0 {
		return ctrl.Result{}, kerrors.NewAggregate(errs)
	}
	return res, nil
}

func (r *AgentReconciler) reconcileServiceAccount(ctx context.Context, agent *v1alpha1.Agent) (ctrl.Result, error) {
	sa := generate.ServiceAccount(agent)
	if err := controllerutil.SetControllerReference(agent, sa, r.Scheme); err != nil {
		return ctrl.Result{}, fmt.Errorf("setting owner reference on ServiceAccount: %w", err)
	}
	if err := r.Patch(ctx, sa, client.Apply, client.FieldOwner(generate.ManagedBy), client.ForceOwnership); err != nil {
		meta.SetStatusCondition(&agent.Status.Conditions, metav1.Condition{
			Type:               "ServiceAccountReady",
			Status:             metav1.ConditionFalse,
			Reason:             "ServiceAccountError",
			Message:            err.Error(),
			LastTransitionTime: metav1.Now(),
		})
		return ctrl.Result{}, fmt.Errorf("applying ServiceAccount %s: %w", agent.Name, err)
	}

	agent.Status.ServiceAccountRef = &corev1.LocalObjectReference{Name: agent.Name}
	meta.SetStatusCondition(&agent.Status.Conditions, metav1.Condition{
		Type:               "ServiceAccountReady",
		Status:             metav1.ConditionTrue,
		Reason:             "Created",
		Message:            fmt.Sprintf("ServiceAccount %s created", agent.Name),
		LastTransitionTime: metav1.Now(),
	})
	return ctrl.Result{}, nil
}

// reconcileDelete just mutates in-memory state. The deferred patch persists the changes.
func (r *AgentReconciler) reconcileDelete(ctx context.Context, agent *v1alpha1.Agent) (ctrl.Result, error) {
	original := agent.DeepCopy()
	controllerutil.RemoveFinalizer(agent, AgentFinalizer)
	return ctrl.Result{}, r.Client.Patch(ctx, agent, client.MergeFrom(original))
}

func (r *AgentReconciler) reconcileCreate(ctx context.Context, agent *v1alpha1.Agent) (ctrl.Result, error) {
	original := agent.DeepCopy()
	controllerutil.AddFinalizer(agent, AgentFinalizer)
	return ctrl.Result{}, r.Client.Patch(ctx, agent, client.MergeFrom(original))
}

func (r *AgentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Agent{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Owns(&corev1.ServiceAccount{}).
		Complete(r)
}
