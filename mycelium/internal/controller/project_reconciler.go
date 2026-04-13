package controller

import (
	"context"
	"fmt"

	v1alpha1 "github.com/mongodb/mycelium/api/v1alpha1"
	"github.com/mongodb/mycelium/internal/generate"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	ProjectFinalizer = "mycelium.io/project-cleanup"
	fieldManager     = "mycelium-controller"
)

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

func (r *ProjectReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var proj v1alpha1.Project
	if err := r.Get(ctx, req.NamespacedName, &proj); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion
	if !proj.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &proj)
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(&proj, ProjectFinalizer) {
		controllerutil.AddFinalizer(&proj, ProjectFinalizer)
		if err := r.Update(ctx, &proj); err != nil {
			return ctrl.Result{}, err
		}
	}

	logger.Info("Reconciling Project", "name", proj.Name)

	// Ensure namespace exists
	if err := r.ensureNamespace(ctx, &proj); err != nil {
		meta.SetStatusCondition(&proj.Status.Conditions, metav1.Condition{
			Type:               "NamespaceReady",
			Status:             metav1.ConditionFalse,
			Reason:             "NamespaceError",
			Message:            err.Error(),
			LastTransitionTime: metav1.Now(),
		})
		_ = r.Status().Update(ctx, &proj)
		return ctrl.Result{}, err
	}
	meta.SetStatusCondition(&proj.Status.Conditions, metav1.Condition{
		Type:               "NamespaceReady",
		Status:             metav1.ConditionTrue,
		Reason:             "Created",
		Message:            fmt.Sprintf("Namespace %s exists", proj.Name),
		LastTransitionTime: metav1.Now(),
	})
	proj.Status.NamespaceRef = &corev1.LocalObjectReference{Name: proj.Name}

	// Generate and apply all AGW resources via SSA into the project's namespace
	if err := r.syncAGWResources(ctx, &proj); err != nil {
		meta.SetStatusCondition(&proj.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "ReconcileError",
			Message:            err.Error(),
			LastTransitionTime: metav1.Now(),
		})
		_ = r.Status().Update(ctx, &proj)
		return ctrl.Result{}, err
	}

	meta.SetStatusCondition(&proj.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "Reconciled",
		Message:            fmt.Sprintf("Project %s reconciled", proj.Name),
		LastTransitionTime: metav1.Now(),
	})
	if err := r.Status().Update(ctx, &proj); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *ProjectReconciler) ensureNamespace(ctx context.Context, proj *v1alpha1.Project) error {
	ns := generate.Namespace(proj)

	if err := controllerutil.SetControllerReference(proj, ns, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on namespace: %w", err)
	}

	if err := r.Patch(ctx, ns, client.Apply, client.FieldOwner(fieldManager), client.ForceOwnership); err != nil {
		return fmt.Errorf("applying namespace %s: %w", proj.Name, err)
	}
	return nil
}

func (r *ProjectReconciler) syncAGWResources(ctx context.Context, proj *v1alpha1.Project) error {
	// List agents and tools for tool-access policy generation
	var agents v1alpha1.AgentList
	if err := r.List(ctx, &agents, client.InNamespace(proj.Name)); err != nil {
		return fmt.Errorf("listing agents: %w", err)
	}
	var tools v1alpha1.ToolList
	if err := r.List(ctx, &tools, client.InNamespace(proj.Name)); err != nil {
		return fmt.Errorf("listing tools: %w", err)
	}

	resources := []client.Object{
		generate.MCPBackend(proj),
		generate.MCPRoute(proj),
		generate.JWTPolicy(proj),
		generate.SourceContextPolicy(proj),
		generate.ToolAccessPolicy(proj, agents.Items, tools.Items),
	}

	for _, obj := range resources {
		if err := controllerutil.SetControllerReference(proj, obj, r.Scheme); err != nil {
			return fmt.Errorf("setting owner reference on %s: %w", obj.GetName(), err)
		}
		if err := r.Patch(ctx, obj, client.Apply, client.FieldOwner(fieldManager), client.ForceOwnership); err != nil {
			return fmt.Errorf("applying %s/%s: %w", obj.GetObjectKind().GroupVersionKind().Kind, obj.GetName(), err)
		}
	}
	return nil
}

func (r *ProjectReconciler) reconcileDelete(ctx context.Context, proj *v1alpha1.Project) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Cleaning up Project", "name", proj.Name)

	// Dependency checks are handled by the ValidatingWebhook at admission time.
	// If we reached here, the webhook already confirmed no dependents exist.

	// TODO(mycelium): Clean up MongoDB project database here

	// Delete the namespace (owned AGW resources cascade via ownerReferences)
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: proj.Name}}
	if err := r.Delete(ctx, ns); err != nil && !errors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	controllerutil.RemoveFinalizer(proj, ProjectFinalizer)
	if err := r.Update(ctx, proj); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// mapToProject maps a namespace-scoped resource to its owning Project reconcile request.
// By convention, the namespace name equals the Project name.
func (r *ProjectReconciler) mapToProject(ctx context.Context, obj client.Object) []reconcile.Request {
	return []reconcile.Request{{
		NamespacedName: types.NamespacedName{Name: obj.GetNamespace()},
	}}
}

func (r *ProjectReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Project{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&v1alpha1.Agent{}, handler.EnqueueRequestsFromMapFunc(r.mapToProject)).
		Watches(&v1alpha1.Tool{}, handler.EnqueueRequestsFromMapFunc(r.mapToProject)).
		Complete(r)
}
