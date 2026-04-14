package controller

import (
	"context"
	"fmt"
	"time"

	v1alpha1 "github.com/mongodb/mycelium/api/v1alpha1"
	"github.com/mongodb/mycelium/internal/generate"
	myceliumutil "github.com/mongodb/mycelium/internal/util"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
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

func (r *ToolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, reterr error) {
	logger := log.FromContext(ctx)

	var tool v1alpha1.Tool
	if err := r.Get(ctx, req.NamespacedName, &tool); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !tool.DeletionTimestamp.IsZero() {
		logger.Info("Cleaning up Tool", "tool", tool.Name)
		return r.reconcileDelete(ctx, &tool)
	}

	if !controllerutil.ContainsFinalizer(&tool, ToolFinalizer) {
		return r.reconcileCreate(ctx, &tool)
	}

	original := tool.DeepCopy()

	defer func() {
		if !equality.Semantic.DeepEqual(original.Status, tool.Status) {
			if err := r.Status().Patch(ctx, &tool, client.MergeFrom(original)); err != nil {
				reterr = kerrors.NewAggregate([]error{reterr, err})
			}
		}
	}()

	logger.Info("Reconciling Tool", "tool", tool.Name)

	var errs []error
	res := ctrl.Result{}
	for _, phase := range []func(context.Context, *v1alpha1.Tool) (ctrl.Result, error){
		r.reconcileProject,
		r.reconcileCredentials,
		r.reconcileService,
	} {
		phaseResult, err := phase(ctx, &tool)
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

func (r *ToolReconciler) reconcileProject(ctx context.Context, tool *v1alpha1.Tool) (ctrl.Result, error) {
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
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("checking Project: %w", err)
	}

	meta.SetStatusCondition(&tool.Status.Conditions, metav1.Condition{
		Type:               "ProjectValid",
		Status:             metav1.ConditionTrue,
		Reason:             "Valid",
		Message:            "Parent project exists",
		LastTransitionTime: metav1.Now(),
	})
	return ctrl.Result{}, nil
}

func (r *ToolReconciler) reconcileCredentials(ctx context.Context, tool *v1alpha1.Tool) (ctrl.Result, error) {
	if len(tool.Spec.Credentials) == 0 {
		meta.SetStatusCondition(&tool.Status.Conditions, metav1.Condition{
			Type:               "CredentialsValid",
			Status:             metav1.ConditionTrue,
			Reason:             "NoCredentials",
			Message:            "No credentials required",
			LastTransitionTime: metav1.Now(),
		})
		return ctrl.Result{}, nil
	}

	for _, cr := range tool.Spec.Credentials {
		msg, err := r.validateCredentialRef(ctx, tool.Namespace, &cr)
		if err != nil {
			return ctrl.Result{}, err
		}
		if msg != "" {
			meta.SetStatusCondition(&tool.Status.Conditions, metav1.Condition{
				Type:               "CredentialsValid",
				Status:             metav1.ConditionFalse,
				Reason:             "InvalidCredentialRef",
				Message:            msg,
				LastTransitionTime: metav1.Now(),
			})
			return ctrl.Result{}, nil
		}
	}

	meta.SetStatusCondition(&tool.Status.Conditions, metav1.Condition{
		Type:               "CredentialsValid",
		Status:             metav1.ConditionTrue,
		Reason:             "Valid",
		Message:            "All credential providers valid",
		LastTransitionTime: metav1.Now(),
	})
	return ctrl.Result{}, nil
}

func (r *ToolReconciler) validateCredentialRef(ctx context.Context, namespace string, cr *v1alpha1.CredentialBinding) (string, error) {
	name := cr.ProviderName()
	var cp v1alpha1.CredentialProvider
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, &cp); err != nil {
		if errors.IsNotFound(err) {
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

func (r *ToolReconciler) reconcileService(ctx context.Context, tool *v1alpha1.Tool) (ctrl.Result, error) {
	knSvc := generate.KnativeService(tool)
	if err := controllerutil.SetControllerReference(tool, knSvc, r.Scheme); err != nil {
		return ctrl.Result{}, fmt.Errorf("setting owner reference on Knative Service: %w", err)
	}
	if err := r.Patch(ctx, knSvc, client.Apply, client.FieldOwner(generate.ManagedBy), client.ForceOwnership); err != nil {
		meta.SetStatusCondition(&tool.Status.Conditions, metav1.Condition{
			Type:               "ServiceReady",
			Status:             metav1.ConditionFalse,
			Reason:             "KnativeServiceError",
			Message:            err.Error(),
			LastTransitionTime: metav1.Now(),
		})
		return ctrl.Result{}, fmt.Errorf("applying Knative Service: %w", err)
	}

	tool.Status.ServiceRef = &corev1.LocalObjectReference{Name: knSvc.Name}
	meta.SetStatusCondition(&tool.Status.Conditions, metav1.Condition{
		Type:               "ServiceReady",
		Status:             metav1.ConditionTrue,
		Reason:             "KnativeServiceCreated",
		Message:            fmt.Sprintf("Knative Service %s created", knSvc.Name),
		LastTransitionTime: metav1.Now(),
	})
	return ctrl.Result{}, nil
}

// reconcileDelete just mutates in-memory state. The deferred patch persists the changes.
func (r *ToolReconciler) reconcileDelete(ctx context.Context, tool *v1alpha1.Tool) (ctrl.Result, error) {
	var agents v1alpha1.AgentList
	if err := r.List(ctx, &agents, client.InNamespace(tool.Namespace),
		client.MatchingFields{IndexAgentToolRefs: tool.Name}); err != nil {
		return ctrl.Result{}, err
	}
	if len(agents.Items) > 0 {
		log.FromContext(ctx).Info("Tool still has dependent Agents, requeuing", "agents", len(agents.Items))
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	original := tool.DeepCopy()
	controllerutil.RemoveFinalizer(tool, ToolFinalizer)
	return ctrl.Result{}, r.Client.Patch(ctx, tool, client.MergeFrom(original))
}

func (r *ToolReconciler) reconcileCreate(ctx context.Context, tool *v1alpha1.Tool) (ctrl.Result, error) {
	original := tool.DeepCopy()
	controllerutil.AddFinalizer(tool, ToolFinalizer)
	return ctrl.Result{}, r.Client.Patch(ctx, tool, client.MergeFrom(original))
}

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
			NamespacedName: types.NamespacedName{Name: tool.Name, Namespace: tool.Namespace},
		})
	}
	return requests
}

func (r *ToolReconciler) findToolsForProject(ctx context.Context, obj client.Object) []ctrl.Request {
	var toolList v1alpha1.ToolList
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
