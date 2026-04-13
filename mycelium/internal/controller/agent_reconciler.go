package controller

import (
	"context"
	"fmt"

	v1alpha1 "github.com/mongodb/mycelium/api/v1alpha1"
	"github.com/mongodb/mycelium/internal/generate"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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

func (r *AgentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var agent v1alpha1.Agent
	if err := r.Get(ctx, req.NamespacedName, &agent); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !agent.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &agent)
	}

	if !controllerutil.ContainsFinalizer(&agent, AgentFinalizer) {
		controllerutil.AddFinalizer(&agent, AgentFinalizer)
		if err := r.Update(ctx, &agent); err != nil {
			return ctrl.Result{}, err
		}
	}

	logger.Info("Reconciling Agent", "agent", agent.Name)

	// Create the per-agent ServiceAccount (used for identity resolution in
	// tool-access policy CEL expressions via source.workload.unverified.serviceAccount)
	if err := r.ensureServiceAccount(ctx, &agent); err != nil {
		meta.SetStatusCondition(&agent.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "ServiceAccountError",
			Message:            err.Error(),
			LastTransitionTime: metav1.Now(),
		})
		_ = r.Status().Update(ctx, &agent)
		return ctrl.Result{}, err
	}
	agent.Status.ServiceAccountRef = &corev1.LocalObjectReference{Name: agent.Name}
	meta.SetStatusCondition(&agent.Status.Conditions, metav1.Condition{
		Type:               "ServiceAccountReady",
		Status:             metav1.ConditionTrue,
		Reason:             "Created",
		Message:            fmt.Sprintf("ServiceAccount %s created", agent.Name),
		LastTransitionTime: metav1.Now(),
	})

	// TODO(mycelium): Generate SandboxTemplate + WarmPool from agent.Spec.Sandbox
	// when agent-sandbox integration is implemented.

	meta.SetStatusCondition(&agent.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "Reconciled",
		Message:            fmt.Sprintf("Agent %s reconciled", agent.Name),
		LastTransitionTime: metav1.Now(),
	})

	if err := r.Status().Update(ctx, &agent); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *AgentReconciler) ensureServiceAccount(ctx context.Context, agent *v1alpha1.Agent) error {
	sa := generate.ServiceAccount(agent)
	if err := controllerutil.SetControllerReference(agent, sa, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on ServiceAccount: %w", err)
	}
	if err := r.Patch(ctx, sa, client.Apply, client.FieldOwner(generate.ManagedBy), client.ForceOwnership); err != nil {
		return fmt.Errorf("applying ServiceAccount %s: %w", agent.Name, err)
	}
	return nil
}

func (r *AgentReconciler) reconcileDelete(ctx context.Context, agent *v1alpha1.Agent) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Cleaning up Agent", "agent", agent.Name)

	// Dependency checks (tool refs) are handled by the ValidatingWebhook.
	// Sandbox resources are cleaned up via ownerReference GC.
	// Tool-access policy is recomputed by the ProjectReconciler watching Agent events.

	controllerutil.RemoveFinalizer(agent, AgentFinalizer)
	if err := r.Update(ctx, agent); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *AgentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Agent{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Owns(&corev1.ServiceAccount{}).
		Complete(r)
}
