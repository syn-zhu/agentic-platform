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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const ToolFinalizer = "mycelium.io/tool-cleanup"

type ToolReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=mycelium.io,resources=tools,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=mycelium.io,resources=tools/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=mycelium.io,resources=tools/finalizers,verbs=update
// +kubebuilder:rbac:groups=serving.knative.dev,resources=services,verbs=get;list;watch;create;update;patch;delete

func (r *ToolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var tool v1alpha1.Tool
	if err := r.Get(ctx, req.NamespacedName, &tool); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion
	if !tool.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &tool)
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(&tool, ToolFinalizer) {
		controllerutil.AddFinalizer(&tool, ToolFinalizer)
		if err := r.Update(ctx, &tool); err != nil {
			return ctrl.Result{}, err
		}
	}

	logger.Info("Reconciling Tool", "tool", tool.Name)

	// Generate and apply Knative Service via SSA
	knSvc := generate.KnativeService(&tool)
	if err := controllerutil.SetControllerReference(&tool, knSvc, r.Scheme); err != nil {
		return ctrl.Result{}, fmt.Errorf("setting owner reference on Knative Service: %w", err)
	}
	if err := r.Patch(ctx, knSvc, client.Apply, client.FieldOwner(fieldManager), client.ForceOwnership); err != nil {
		meta.SetStatusCondition(&tool.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "KnativeServiceError",
			Message:            err.Error(),
			LastTransitionTime: metav1.Now(),
		})
		_ = r.Status().Update(ctx, &tool)
		return ctrl.Result{}, fmt.Errorf("applying Knative Service: %w", err)
	}

	// Update status with Knative Service ref
	tool.Status.ServiceRef = &corev1.LocalObjectReference{
		Name: knSvc.Name,
	}

	// TODO(mycelium): Recompute tool-access AgentgatewayPolicy CEL expressions.
	// This requires listing all agents that reference this tool and regenerating
	// the mcp-tool-access policy. Deferred until agent definition CRD is integrated.

	meta.SetStatusCondition(&tool.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "Reconciled",
		Message:            fmt.Sprintf("Knative Service %s created", knSvc.Name),
		LastTransitionTime: metav1.Now(),
	})
	if err := r.Status().Update(ctx, &tool); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *ToolReconciler) reconcileDelete(ctx context.Context, tool *v1alpha1.Tool) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Cleaning up Tool", "tool", tool.Name)

	// TODO(mycelium): Remove this tool's entry from the mcp-tool-access policy
	// CEL expressions when agent definition CRD is integrated.

	// Knative Service is cleaned up via ownerReference GC.

	controllerutil.RemoveFinalizer(tool, ToolFinalizer)
	if err := r.Update(ctx, tool); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *ToolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Tool{}).
		Complete(r)
}
