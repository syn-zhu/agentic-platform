package controller

import (
	"context"
	"fmt"

	v1alpha1 "mycelium.io/mycelium/api/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
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
)

const CredentialProviderFinalizer = "mycelium.io/credentialprovider-cleanup"

type CredentialProviderReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=mycelium.io,resources=credentialproviders,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=mycelium.io,resources=credentialproviders/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=mycelium.io,resources=credentialproviders/finalizers,verbs=update
// +kubebuilder:rbac:groups=mycelium.io,resources=projects,verbs=get;list;watch
// +kubebuilder:rbac:groups=mycelium.io,resources=tools,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *CredentialProviderReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, retErr error) {
	logger := log.FromContext(ctx)

	var cp v1alpha1.CredentialProvider
	if err := r.Get(ctx, req.NamespacedName, &cp); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	patchHelper, err := patch.NewHelper(&cp, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}

	defer func() {
		if err := patchHelper.Patch(ctx, &cp,
			patch.WithOwnedConditions{Conditions: []string{v1alpha1.ReadyCondition}},
			patch.WithStatusObservedGeneration{},
		); err != nil {
			retErr = kerrors.NewAggregate([]error{retErr, err})
		}
	}()

	if !cp.DeletionTimestamp.IsZero() {
		logger.Info("Cleaning up CredentialProvider", "name", cp.Name)
		return r.reconcileDelete(ctx, &cp)
	}

	logger.Info("Reconciling CredentialProvider", "name", cp.Name, "type", cp.Spec.Type)

	// Prerequisites: validate sequentially, fail early.
	// Bad state (dependency not found): condition is set, return nil — watch will retrigger.
	// Unexpected error: return error to requeue with backoff.

	// TODO: actually, I think we should do the same thing as in the project reconciler
	// and just accumulate all errors.
	if ok, err := r.resolveProject(ctx, &cp); !ok {
		return ctrl.Result{}, err
	}
	if ok, err := r.resolveSecrets(ctx, &cp); !ok {
		return ctrl.Result{}, err
	}

	// No owned resources to reconcile for CredentialProvider.
	meta.SetStatusCondition(&cp.Status.Conditions, metav1.Condition{
		Type:    v1alpha1.ReadyCondition,
		Status:  metav1.ConditionTrue,
		Reason:  v1alpha1.SucceededReason,
		Message: "All prerequisites valid",
	})
	return ctrl.Result{}, nil
}

// resolveProject checks that the parent Project exists.
// Returns (false, nil) on not-found or deleting after setting Ready=False — caller should return without requeue.
// Returns (false, err) on unexpected API errors.
func (r *CredentialProviderReconciler) resolveProject(ctx context.Context, cp *v1alpha1.CredentialProvider) (bool, error) {
	var proj v1alpha1.Project
	if err := r.Get(ctx, types.NamespacedName{Name: cp.Namespace}, &proj); err != nil {
		if errors.IsNotFound(err) {
			meta.SetStatusCondition(&cp.Status.Conditions, metav1.Condition{
				Type:    v1alpha1.ReadyCondition,
				Status:  metav1.ConditionFalse,
				Reason:  v1alpha1.FailedReason,
				Message: fmt.Sprintf("Project %s not found", cp.Namespace),
			})
			return false, nil
		}
		return false, fmt.Errorf("checking Project: %w", err)
	}
	return true, nil
	// TODO: we should add a field to the Status here to indicate that the project is resolved
}

// validateSecrets checks that the referenced K8s Secret exists and is not being deleted.
// Returns (false, nil) on not-found/deleting after setting Ready=False — caller should return without requeue.
// Returns (false, err) on unexpected API errors.
func (r *CredentialProviderReconciler) resolveSecrets(ctx context.Context, cp *v1alpha1.CredentialProvider) (bool, error) {
	var sel corev1.SecretKeySelector
	switch cp.Spec.Type {
	case v1alpha1.CredentialProviderTypeOAuth:
		sel = cp.Spec.OAuth.ClientSecretRef
	case v1alpha1.CredentialProviderTypeAPIKey:
		sel = cp.Spec.APIKey.APIKeySecretRef
	}

	var secret corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Name: sel.Name, Namespace: cp.Namespace}, &secret); err != nil {
		if errors.IsNotFound(err) {
			meta.SetStatusCondition(&cp.Status.Conditions, metav1.Condition{
				Type:    v1alpha1.ReadyCondition,
				Status:  metav1.ConditionFalse,
				Reason:  v1alpha1.FailedReason,
				Message: fmt.Sprintf("Secret %s not found", sel.Name),
			})
			return false, nil
		}
		return false, fmt.Errorf("checking Secret %s: %w", sel.Name, err)
	}
	return true, nil
}

func (r *CredentialProviderReconciler) reconcileDelete(ctx context.Context, cp *v1alpha1.CredentialProvider) (ctrl.Result, error) {
	var tools v1alpha1.ToolList
	if err := r.List(ctx, &tools, client.InNamespace(cp.Namespace),
		client.MatchingFields{ToolCredentialBindingIndex(cp.Spec.Type): cp.Name}); err != nil {
		return ctrl.Result{}, err
	}
	if len(tools.Items) > 0 {
		// TODO: set our status to terminating
		// meta.SetStatusCondition(&cp.Status.Conditions, )
		log.FromContext(ctx).Info("CredentialProvider still has dependent Tools", "tools", len(tools.Items))
		return ctrl.Result{}, nil
	}

	controllerutil.RemoveFinalizer(cp, CredentialProviderFinalizer)
	return ctrl.Result{}, nil
}

func (r *CredentialProviderReconciler) mapToolToCredentialProviders(_ context.Context, obj client.Object) []ctrl.Request {
	tool, ok := obj.(*v1alpha1.Tool)
	if !ok {
		return nil
	}
	requests := make([]ctrl.Request, 0, len(tool.Spec.CredentialBindings))
	for _, cr := range tool.Spec.CredentialBindings {
		requests = append(requests, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: cr.CredentialProviderName(), Namespace: tool.Namespace},
		})
	}
	return requests
}

func (r *CredentialProviderReconciler) mapSecretToCredentialProviders(ctx context.Context, obj client.Object) []ctrl.Request {
	var cpList v1alpha1.CredentialProviderList
	if err := r.List(ctx, &cpList,
		client.InNamespace(obj.GetNamespace()),
		client.MatchingFields{IndexCredentialProviderSecrets: obj.GetName()},
	); err != nil {
		return nil
	}
	requests := make([]ctrl.Request, 0, len(cpList.Items))
	for _, cp := range cpList.Items {
		requests = append(requests, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: cp.Name, Namespace: cp.Namespace},
		})
	}
	return requests
}

func (r *CredentialProviderReconciler) mapProjectToCredentialProviders(ctx context.Context, obj client.Object) []ctrl.Request {
	var cpList v1alpha1.CredentialProviderList
	if err := r.List(ctx, &cpList, client.InNamespace(obj.GetName())); err != nil {
		return nil
	}
	requests := make([]ctrl.Request, 0, len(cpList.Items))
	for _, cp := range cpList.Items {
		requests = append(requests, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: cp.Name, Namespace: cp.Namespace},
		})
	}
	return requests
}

func (r *CredentialProviderReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.CredentialProvider{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&v1alpha1.Project{},
			handler.EnqueueRequestsFromMapFunc(r.mapProjectToCredentialProviders),
		).
		Watches(&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.mapSecretToCredentialProviders),
		).
		Watches(&v1alpha1.Tool{},
			handler.EnqueueRequestsFromMapFunc(r.mapToolToCredentialProviders),
		).
		Complete(r)
}
