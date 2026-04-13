package controller

import (
	"context"
	"fmt"
	"time"

	v1alpha1 "github.com/mongodb/mycelium/api/v1alpha1"
	"github.com/mongodb/mycelium/internal/generate"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	knservingv1 "knative.dev/serving/pkg/apis/serving/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const ToolFinalizer = "mycelium.io/tool-cleanup"

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

	// Validate parent Project and set ProjectValid condition.
	// Returns error only for transient API failures.
	if err := r.reconcileProject(ctx, &tool); err != nil {
		_ = r.Status().Update(ctx, &tool)
		return ctrl.Result{}, err
	}

	// Validate credential refs and set CredentialsValid condition.
	// Returns error only for transient API failures.
	if err := r.reconcileCredentials(ctx, &tool); err != nil {
		_ = r.Status().Update(ctx, &tool)
		return ctrl.Result{}, err
	}

	// Generate and apply Knative Service, set ServiceReady condition.
	// Returns error only for transient API failures.
	if err := r.reconcileService(ctx, &tool); err != nil {
		r.setReadyCondition(&tool)
		_ = r.Status().Update(ctx, &tool)
		return ctrl.Result{}, err
	}

	// Tool-access policy recomputation is handled by the ProjectReconciler,
	// which watches Tool events and regenerates the policy automatically.

	// Set rollup Ready condition and persist status
	r.setReadyCondition(&tool)
	if err := r.Status().Update(ctx, &tool); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// reconcileProject checks that the parent Project exists.
// Returns error only for transient API failures.
func (r *ToolReconciler) reconcileProject(ctx context.Context, tool *v1alpha1.Tool) error {
	var proj v1alpha1.Project
	if err := r.Get(ctx, types.NamespacedName{Name: tool.Namespace}, &proj); err != nil {
		if errors.IsNotFound(err) {
			meta.SetStatusCondition(&tool.Status.Conditions, metav1.Condition{
				Type:               "ProjectValid",
				Status:             metav1.ConditionFalse,
				Reason:             "ProjectNotFound",
				Message:            fmt.Sprintf("Project %s not found", tool.Namespace),
				LastTransitionTime: metav1.Now(),
			})
			return nil
		}
		return fmt.Errorf("checking Project: %w", err)
	}

	meta.SetStatusCondition(&tool.Status.Conditions, metav1.Condition{
		Type:               "ProjectValid",
		Status:             metav1.ConditionTrue,
		Reason:             "Valid",
		Message:            "Parent project exists",
		LastTransitionTime: metav1.Now(),
	})
	return nil
}

// reconcileCredentials validates all credential provider refs and sets the
// CredentialsValid condition. Returns error only for transient API failures.
func (r *ToolReconciler) reconcileCredentials(ctx context.Context, tool *v1alpha1.Tool) error {
	if len(tool.Spec.Credentials) == 0 {
		meta.SetStatusCondition(&tool.Status.Conditions, metav1.Condition{
			Type:               "CredentialsValid",
			Status:             metav1.ConditionTrue,
			Reason:             "NoCredentials",
			Message:            "No credentials required",
			LastTransitionTime: metav1.Now(),
		})
		return nil
	}

	for _, cr := range tool.Spec.Credentials {
		msg, err := r.validateCredentialRef(ctx, tool.Namespace, &cr)
		if err != nil {
			return err // transient API error — retry
		}
		if msg != "" {
			meta.SetStatusCondition(&tool.Status.Conditions, metav1.Condition{
				Type:               "CredentialsValid",
				Status:             metav1.ConditionFalse,
				Reason:             "InvalidCredentialRef",
				Message:            msg,
				LastTransitionTime: metav1.Now(),
			})
			return nil
		}
	}

	meta.SetStatusCondition(&tool.Status.Conditions, metav1.Condition{
		Type:               "CredentialsValid",
		Status:             metav1.ConditionTrue,
		Reason:             "Valid",
		Message:            "All credential providers valid",
		LastTransitionTime: metav1.Now(),
	})
	return nil
}

// validateCredentialRef checks a single credential ref against the cluster.
// Returns (message, nil) for validation failures (surfaced as conditions),
// or ("", err) for transient API errors (should trigger retry).
func (r *ToolReconciler) validateCredentialRef(ctx context.Context, namespace string, cr *v1alpha1.CredentialBinding) (string, error) {
	name := cr.ProviderName()
	var cp v1alpha1.CredentialProvider
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, &cp); err != nil {
		if errors.IsNotFound(err) {
			// TODO: check all errors using ReasonForError
			return fmt.Sprintf("CredentialProvider %s not found", name), nil
		}
		return "", fmt.Errorf("checking CredentialProvider %s: %w", name, err)
	}
	if cr.IsOAuth() && !cp.IsOAuth() {
		return fmt.Sprintf("CredentialProvider %s is not an OAuth provider", name), nil
	}
	if cr.IsAPIKey() && !cp.IsAPIKey() {
		return fmt.Sprintf("CredentialProvider %s is not an API key provider", name), nil
	}
	return "", nil
}

// reconcileService generates and applies the Knative Service via SSA, and sets
// the ServiceReady condition. Returns error only for transient failures.
func (r *ToolReconciler) reconcileService(ctx context.Context, tool *v1alpha1.Tool) error {
	knSvc := generate.KnativeService(tool)
	if err := controllerutil.SetControllerReference(tool, knSvc, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on Knative Service: %w", err)
	}
	if err := r.Patch(ctx, knSvc, client.Apply, client.FieldOwner(generate.ManagedBy), client.ForceOwnership); err != nil {
		meta.SetStatusCondition(&tool.Status.Conditions, metav1.Condition{
			Type:               "ServiceReady",
			Status:             metav1.ConditionFalse,
			Reason:             "KnativeServiceError",
			Message:            err.Error(),
			LastTransitionTime: metav1.Now(),
		})
		return fmt.Errorf("applying Knative Service: %w", err)
	}

	tool.Status.ServiceRef = &corev1.LocalObjectReference{Name: knSvc.Name}
	meta.SetStatusCondition(&tool.Status.Conditions, metav1.Condition{
		Type:               "ServiceReady",
		Status:             metav1.ConditionTrue,
		Reason:             "KnativeServiceCreated",
		Message:            fmt.Sprintf("Knative Service %s created", knSvc.Name),
		LastTransitionTime: metav1.Now(),
	})
	return nil
}

// setReadyCondition computes the rollup Ready condition from sub-conditions.
func (r *ToolReconciler) setReadyCondition(tool *v1alpha1.Tool) {
	projValid := meta.IsStatusConditionTrue(tool.Status.Conditions, "ProjectValid")
	credsValid := meta.IsStatusConditionTrue(tool.Status.Conditions, "CredentialsValid")
	svcReady := meta.IsStatusConditionTrue(tool.Status.Conditions, "ServiceReady")

	if projValid && credsValid && svcReady {
		meta.SetStatusCondition(&tool.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionTrue,
			Reason:             "Reconciled",
			Message:            "All sub-conditions satisfied",
			LastTransitionTime: metav1.Now(),
		})
	} else {
		var reason string
		switch {
		case !projValid:
			reason = "ProjectInvalid"
		case !credsValid:
			reason = "CredentialsInvalid"
		default:
			reason = "ServiceNotReady"
		}
		meta.SetStatusCondition(&tool.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             reason,
			Message:            "One or more sub-conditions not satisfied",
			LastTransitionTime: metav1.Now(),
		})
	}
}

func (r *ToolReconciler) reconcileDelete(ctx context.Context, tool *v1alpha1.Tool) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Cleaning up Tool", "tool", tool.Name)

	// Wait for dependent Agents to be removed before finalizing.
	var agents v1alpha1.AgentList
	if err := r.List(ctx, &agents, client.InNamespace(tool.Namespace),
		client.MatchingFields{IndexAgentToolRefs: tool.Name}); err != nil {
		return ctrl.Result{}, err
	}
	if len(agents.Items) > 0 {
		logger.Info("Tool still has dependent Agents, requeuing",
			"agents", len(agents.Items))
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	controllerutil.RemoveFinalizer(tool, ToolFinalizer)
	if err := r.Update(ctx, tool); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// findToolsForCredentialProvider maps a CredentialProvider event to the Tools
// that reference it, so those Tools get re-reconciled.
func (r *ToolReconciler) findToolsForCredentialProvider(ctx context.Context, obj client.Object) []ctrl.Request {
	var toolList v1alpha1.ToolList
	if err := r.List(ctx, &toolList,
		client.InNamespace(obj.GetNamespace()),
		client.MatchingFields{IndexToolCredentialBindings: obj.GetName()},
	); err != nil {
		return nil
	}
	requests := make([]ctrl.Request, 0, len(toolList.Items))
	for _, tool := range toolList.Items {
		requests = append(requests, ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      tool.Name,
				Namespace: tool.Namespace,
			},
		})
	}
	return requests
}

// findToolsForProject maps a Project event to all Tools in that project's namespace.
func (r *ToolReconciler) findToolsForProject(ctx context.Context, obj client.Object) []ctrl.Request {
	var toolList v1alpha1.ToolList
	if err := r.List(ctx, &toolList, client.InNamespace(obj.GetName())); err != nil {
		return nil
	}
	requests := make([]ctrl.Request, 0, len(toolList.Items))
	for _, tool := range toolList.Items {
		requests = append(requests, ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      tool.Name,
				Namespace: tool.Namespace,
			},
		})
	}
	return requests
}

func (r *ToolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Tool{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Owns(&knservingv1.Service{}).
		Watches(&v1alpha1.CredentialProvider{},
			handler.EnqueueRequestsFromMapFunc(r.findToolsForCredentialProvider),
		).
		Watches(&v1alpha1.Project{},
			handler.EnqueueRequestsFromMapFunc(r.findToolsForProject),
		).
		Complete(r)
}
