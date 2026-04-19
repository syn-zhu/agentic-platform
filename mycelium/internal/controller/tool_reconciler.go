package controller

import (
	"context"
	"fmt"
	"time"

	v1alpha1 "mycelium.io/mycelium/api/v1alpha1"
	"mycelium.io/mycelium/internal/generate"
	"mycelium.io/mycelium/internal/indexes"
	"mycelium.io/mycelium/pkg/wellknown"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	knapis "knative.dev/pkg/apis"
	knservingv1 "knative.dev/serving/pkg/apis/serving/v1"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const ToolFinalizer = "mycelium.io/tool-cleanup"

// phaseStatus is a local summary of a reconcileService outcome.
// It is scoped to the tool reconciler and distinct from the stage/condition
// pattern used by the project DAG.
type phaseStatus int

const (
	phaseInProgress phaseStatus = iota
	phaseSucceeded
	phaseFailed
)

type ToolReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=mycelium.io,resources=tools,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=mycelium.io,resources=tools/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=mycelium.io,resources=tools/finalizers,verbs=update
// +kubebuilder:rbac:groups=mycelium.io,resources=projects,verbs=get;list;watch
// +kubebuilder:rbac:groups=mycelium.io,resources=credentialproviders,verbs=get;list;watch
// +kubebuilder:rbac:groups=serving.knative.dev,resources=services,verbs=get;list;watch;create;update;patch;delete

func (r *ToolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, reterr error) {
	logger := log.FromContext(ctx)

	var tool v1alpha1.MyceliumTool
	if err := r.Get(ctx, req.NamespacedName, &tool); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	patchHelper, err := patch.NewHelper(&tool, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}

	defer func() {
		// Only set Ready on error here; on success it is set inline based on phaseStatus.
		if tool.DeletionTimestamp.IsZero() && reterr != nil {
			apimeta.SetStatusCondition(&tool.Status.Conditions, metav1.Condition{
				Type:    v1alpha1.EcosystemReadyCondition,
				Status:  metav1.ConditionFalse,
				Reason:  v1alpha1.FailedReason,
				Message: fmt.Sprintf("Failed to reconcile: %v", reterr),
			})
		}
		if err := patchHelper.Patch(ctx, &tool,
			patch.WithOwnedConditions{Conditions: []string{v1alpha1.EcosystemReadyCondition, v1alpha1.KnativeServiceCondition}},
			patch.WithStatusObservedGeneration{},
		); err != nil {
			reterr = kerrors.NewAggregate([]error{reterr, err})
		}
	}()

	if !tool.DeletionTimestamp.IsZero() {
		logger.Info("Cleaning up Tool", "tool", tool.Name)
		return r.reconcileDelete(ctx, &tool)
	}

	logger.Info("Reconciling Tool", "tool", tool.Name)

	// Prerequisites: validate sequentially, fail early.
	if err := r.resolveProject(ctx, &tool); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.resolveCredentialBindings(ctx, &tool); err != nil {
		return ctrl.Result{}, err
	}

	// Owned resources.
	st, err := r.reconcileService(ctx, &tool)
	if err != nil {
		return ctrl.Result{}, err
	}

	switch st {
	case phaseSucceeded:
		apimeta.SetStatusCondition(&tool.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.EcosystemReadyCondition,
			Status:  metav1.ConditionTrue,
			Reason:  v1alpha1.RunningReason,
			Message: fmt.Sprintf("Tool %s reconciled", tool.Name),
		})
	case phaseInProgress:
		apimeta.SetStatusCondition(&tool.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.EcosystemReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha1.ProgressingReason,
			Message: "Waiting for Knative Service to become ready",
		})
	case phaseFailed:
		apimeta.SetStatusCondition(&tool.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.EcosystemReadyCondition,
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha1.FailedReason,
			Message: "Knative Service failed",
		})
	}
	return ctrl.Result{}, nil
}

// validateProject checks that the parent Project exists.
func (r *ToolReconciler) resolveProject(ctx context.Context, tool *v1alpha1.MyceliumTool) error {
	var proj v1alpha1.MyceliumEcosystem
	if err := r.Get(ctx, types.NamespacedName{Name: tool.Namespace}, &proj); err != nil {
		if apierrors.IsNotFound(err) {
			// TODO: set status
			return fmt.Errorf("Project %s not found", tool.Namespace)
		}
		return fmt.Errorf("checking Project: %w", err)
	}
	return nil
}

// resolveCredentialBindings checks that each referenced CredentialProvider exists and has the correct type.
func (r *ToolReconciler) resolveCredentialBindings(ctx context.Context, tool *v1alpha1.MyceliumTool) error {
	for _, cr := range tool.Spec.CredentialProviderBindings {
		name := cr.CredentialProviderName()

		var cp v1alpha1.MyceliumCredentialProvider
		if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: tool.Namespace}, &cp); err != nil {
			if apierrors.IsNotFound(err) {
				return fmt.Errorf("CredentialProvider %s not found", name)
			}
			return fmt.Errorf("checking CredentialProvider %s: %w", name, err)
		}
		if cp.Spec.Type != cr.Type {
			return fmt.Errorf("CredentialProvider %s has type %s, binding expects %s", name, cp.Spec.Type, cr.Type)
		}
	}
	return nil
}

// reconcileService ensures the Knative Service for this tool exists and propagates its readiness.
func (r *ToolReconciler) reconcileService(ctx context.Context, tool *v1alpha1.MyceliumTool) (phaseStatus, error) {
	knSvc := generate.KnativeService(tool)
	if err := controllerutil.SetControllerReference(tool, knSvc, r.Scheme); err != nil {
		return phaseInProgress, fmt.Errorf("setting owner reference on Knative Service: %w", err)
	}
	if err := r.Patch(ctx, knSvc, client.Apply, client.FieldOwner(wellknown.MyceliumControllerName), client.ForceOwnership); err != nil {
		return phaseInProgress, fmt.Errorf("applying Knative Service %s: %w", knSvc.Name, err)
	}

	tool.Status.Service = &corev1.TypedLocalObjectReference{
		Kind: "Service",
		Name: knSvc.Name,
	}

	// Read back the current status set by the Knative controller.
	var svc knservingv1.Service
	if err := r.Get(ctx, types.NamespacedName{Name: knSvc.Name, Namespace: tool.Namespace}, &svc); err != nil {
		if apierrors.IsNotFound(err) {
			// Just applied; cache hasn't caught up yet. The Owns() watch will re-trigger.
			apimeta.SetStatusCondition(&tool.Status.Conditions, metav1.Condition{
				Type:    v1alpha1.KnativeServiceCondition,
				Status:  metav1.ConditionFalse,
				Reason:  v1alpha1.ProgressingReason,
				Message: "Waiting for Knative Service to be created",
			})
			return phaseInProgress, nil
		}
		return phaseInProgress, fmt.Errorf("getting Knative Service %s: %w", knSvc.Name, err)
	}

	cond := svc.Status.GetCondition(knapis.ConditionReady)
	switch {
	case cond == nil || cond.IsUnknown():
		msg := "Knative Service not yet ready"
		if cond != nil && cond.Message != "" {
			msg = cond.Message
		}
		apimeta.SetStatusCondition(&tool.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.KnativeServiceCondition,
			Status:  metav1.ConditionFalse,
			Reason:  v1alpha1.ProgressingReason,
			Message: msg,
		})
		return phaseInProgress, nil
	case cond.IsTrue():
		apimeta.SetStatusCondition(&tool.Status.Conditions, metav1.Condition{
			Type:   v1alpha1.KnativeServiceCondition,
			Status: metav1.ConditionTrue,
			Reason: v1alpha1.RunningReason,
		})
		return phaseSucceeded, nil
	default: // IsFalse
		reason := cond.Reason
		if reason == "" {
			reason = v1alpha1.FailedReason
		}
		apimeta.SetStatusCondition(&tool.Status.Conditions, metav1.Condition{
			Type:    v1alpha1.KnativeServiceCondition,
			Status:  metav1.ConditionFalse,
			Reason:  reason,
			Message: cond.Message,
		})
		return phaseFailed, nil
	}
}

func (r *ToolReconciler) reconcileDelete(ctx context.Context, tool *v1alpha1.MyceliumTool) (ctrl.Result, error) {
	var agents v1alpha1.MyceliumAgentList
	if err := r.List(ctx, &agents, client.InNamespace(tool.Namespace),
		client.MatchingFields{indexes.IndexAgentToolBindings: tool.Name}); err != nil {
		return ctrl.Result{}, err
	}
	if len(agents.Items) > 0 {
		log.FromContext(ctx).Info("Tool still has dependent Agents, requeuing", "agents", len(agents.Items))
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	controllerutil.RemoveFinalizer(tool, ToolFinalizer)
	return ctrl.Result{}, nil
}

func (r *ToolReconciler) findToolsForCredentialProvider(ctx context.Context, obj client.Object) []ctrl.Request {
	cp, ok := obj.(*v1alpha1.MyceliumCredentialProvider)
	if !ok {
		return nil
	}
	var toolList v1alpha1.MyceliumToolList
	if err := r.List(ctx, &toolList,
		client.InNamespace(cp.Namespace),
		client.MatchingFields{indexes.ToolCredentialBindingIndex(cp.Spec.Type): cp.Name},
	); err != nil {
		return nil
	}
	requests := make([]ctrl.Request, 0, len(toolList.Items))
	for _, tool := range toolList.Items {
		requests = append(requests, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: tool.Name, Namespace: tool.Namespace},
		})
	}
	return requests
}

func (r *ToolReconciler) findToolsForProject(ctx context.Context, obj client.Object) []ctrl.Request {
	var toolList v1alpha1.MyceliumToolList
	if err := r.List(ctx, &toolList, client.InNamespace(obj.GetName())); err != nil {
		return nil
	}
	requests := make([]ctrl.Request, 0, len(toolList.Items))
	for _, tool := range toolList.Items {
		requests = append(requests, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: tool.Name, Namespace: tool.Namespace},
		})
	}
	return requests
}

func (r *ToolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.MyceliumTool{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Owns(&knservingv1.Service{}).
		Watches(&v1alpha1.MyceliumCredentialProvider{},
			handler.EnqueueRequestsFromMapFunc(r.findToolsForCredentialProvider),
		).
		Watches(&v1alpha1.MyceliumEcosystem{},
			handler.EnqueueRequestsFromMapFunc(r.findToolsForProject),
		).
		Complete(r)
}
