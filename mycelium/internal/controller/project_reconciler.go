package controller

import (
	"context"
	"fmt"
	"slices"
	"time"

	v1alpha1 "github.com/mongodb/mycelium/api/v1alpha1"
	"github.com/mongodb/mycelium/internal/generate"
	myceliumutil "github.com/mongodb/mycelium/internal/util"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
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

func (r *ProjectReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, reterr error) {
	logger := log.FromContext(ctx)

	var proj v1alpha1.Project
	if err := r.Get(ctx, req.NamespacedName, &proj); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !proj.DeletionTimestamp.IsZero() {
		logger.Info("Cleaning up Project", "name", proj.Name)
		return r.reconcileDelete(ctx, &proj)
	}

	if controllerutil.AddFinalizer(&proj, ProjectFinalizer) {
		return r.reconcileCreate(ctx, &proj)
	}

	original := proj.DeepCopy()

	defer func() {
		if !equality.Semantic.DeepEqual(original.Status, proj.Status) {
			if err := r.Status().Patch(ctx, &proj, client.MergeFrom(original)); err != nil {
				reterr = kerrors.NewAggregate([]error{reterr, err})
			}
		}
	}()

	logger.Info("Reconciling Project", "name", proj.Name)

	var errs []error
	res := ctrl.Result{}
	for _, phase := range []func(context.Context, *v1alpha1.Project) (ctrl.Result, error){
		r.reconcileNamespace,
		r.reconcileAGWResources,
	} {
		phaseResult, err := phase(ctx, &proj)
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

func (r *ProjectReconciler) reconcileNamespace(ctx context.Context, proj *v1alpha1.Project) (ctrl.Result, error) {
	ns := generate.Namespace(proj)
	if err := controllerutil.SetControllerReference(proj, ns, r.Scheme); err != nil {
		return ctrl.Result{}, fmt.Errorf("setting owner reference on namespace: %w", err)
	}
	if err := r.Patch(ctx, ns, client.Apply, client.FieldOwner(generate.ManagedBy), client.ForceOwnership); err != nil {
		meta.SetStatusCondition(&proj.Status.Conditions, metav1.Condition{
			Type:               "NamespaceReady",
			Status:             metav1.ConditionFalse,
			Reason:             "NamespaceError",
			Message:            err.Error(),
			LastTransitionTime: metav1.Now(),
		})
		return ctrl.Result{}, fmt.Errorf("applying namespace %s: %w", proj.Name, err)
	}

	meta.SetStatusCondition(&proj.Status.Conditions, metav1.Condition{
		Type:               "NamespaceReady",
		Status:             metav1.ConditionTrue,
		Reason:             "Created",
		Message:            fmt.Sprintf("Namespace %s created", proj.Name),
		LastTransitionTime: metav1.Now(),
	})
	proj.Status.NamespaceRef = &corev1.LocalObjectReference{Name: proj.Name}
	return ctrl.Result{}, nil
}

func (r *ProjectReconciler) reconcileAGWResources(ctx context.Context, proj *v1alpha1.Project) (ctrl.Result, error) {
	var agents v1alpha1.AgentList
	if err := r.List(ctx, &agents, client.InNamespace(proj.Name)); err != nil {
		return ctrl.Result{}, fmt.Errorf("listing agents: %w", err)
	}

	resources := []client.Object{
		generate.MCPBackend(proj),
		generate.MCPRoute(proj),
		generate.JWTPolicy(proj),
		generate.SourceContextPolicy(proj),
		generate.ToolAccessPolicy(proj, agents.Items),
	}

	for _, obj := range resources {
		if err := controllerutil.SetControllerReference(proj, obj, r.Scheme); err != nil {
			return ctrl.Result{}, fmt.Errorf("setting owner reference on %s: %w", obj.GetName(), err)
		}
		if err := r.Patch(ctx, obj, client.Apply, client.FieldOwner(generate.ManagedBy), client.ForceOwnership); err != nil {
			meta.SetStatusCondition(&proj.Status.Conditions, metav1.Condition{
				Type:               "AGWResourcesReady",
				Status:             metav1.ConditionFalse,
				Reason:             "AGWResourceError",
				Message:            err.Error(),
				LastTransitionTime: metav1.Now(),
			})
			return ctrl.Result{}, fmt.Errorf("applying %s/%s: %w", obj.GetObjectKind().GroupVersionKind().Kind, obj.GetName(), err)
		}
	}

	meta.SetStatusCondition(&proj.Status.Conditions, metav1.Condition{
		Type:               "AGWResourcesReady",
		Status:             metav1.ConditionTrue,
		Reason:             "Synced",
		Message:            "All AGW resources synced",
		LastTransitionTime: metav1.Now(),
	})
	return ctrl.Result{}, nil
}

// reconcileDelete just mutates in-memory state. The deferred patch persists the changes.
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
		log.FromContext(ctx).Info("Project still has dependents, requeuing",
			"tools", len(tools.Items), "cps", len(cps.Items), "agents", len(agents.Items))
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	original := proj.DeepCopy()
	controllerutil.RemoveFinalizer(proj, ProjectFinalizer)
	return ctrl.Result{}, r.Client.Patch(ctx, proj, client.MergeFrom(original))
}

func (r *ProjectReconciler) reconcileCreate(ctx context.Context, proj *v1alpha1.Project) (ctrl.Result, error) {
	original := proj.DeepCopy()
	controllerutil.AddFinalizer(proj, ProjectFinalizer)
	return ctrl.Result{}, r.Client.Patch(ctx, proj, client.MergeFrom(original))
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
			return !ok || len(agent.Spec.Tools) > 0
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			agent, ok := e.Object.(*v1alpha1.Agent)
			return !ok || len(agent.Spec.Tools) > 0
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldAgent, ok := e.ObjectOld.(*v1alpha1.Agent)
			if !ok {
				return true
			}
			newAgent, ok := e.ObjectNew.(*v1alpha1.Agent)
			if !ok {
				return true
			}
			return !slices.Equal(oldAgent.Spec.Tools, newAgent.Spec.Tools)
		},
	}
}

func (r *ProjectReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Project{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&v1alpha1.Agent{}, handler.EnqueueRequestsFromMapFunc(r.mapToProject),
			builder.WithPredicates(agentToolListChanged())).
		Complete(r)
}
