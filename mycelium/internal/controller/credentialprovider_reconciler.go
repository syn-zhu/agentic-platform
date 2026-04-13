package controller

import (
	"context"
	"fmt"

	v1alpha1 "github.com/mongodb/mycelium/api/v1alpha1"

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

const CredentialProviderFinalizer = "mycelium.io/credentialprovider-cleanup"

type CredentialProviderReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=mycelium.io,resources=credentialproviders,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=mycelium.io,resources=credentialproviders/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=mycelium.io,resources=credentialproviders/finalizers,verbs=update
// +kubebuilder:rbac:groups=mycelium.io,resources=tools,verbs=get;list;watch

func (r *CredentialProviderReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var cp v1alpha1.CredentialProvider
	if err := r.Get(ctx, req.NamespacedName, &cp); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion
	if !cp.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &cp)
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(&cp, CredentialProviderFinalizer) {
		controllerutil.AddFinalizer(&cp, CredentialProviderFinalizer)
		if err := r.Update(ctx, &cp); err != nil {
			return ctrl.Result{}, err
		}
	}

	logger.Info("Reconciling CredentialProvider", "name", cp.Name, "isOAuth", cp.IsOAuth(), "isAPIKey", cp.IsAPIKey())

	// Set Ready condition
	meta.SetStatusCondition(&cp.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "Reconciled",
		Message:            fmt.Sprintf("CredentialProvider %s/%s reconciled", cp.Namespace, cp.Name),
		LastTransitionTime: metav1.Now(),
	})
	if err := r.Status().Update(ctx, &cp); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *CredentialProviderReconciler) reconcileDelete(ctx context.Context, cp *v1alpha1.CredentialProvider) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Cleaning up CredentialProvider", "name", cp.Name)

	// Dependency checks are handled by the ValidatingWebhook at admission time.
	// If we reached here, the webhook already confirmed no dependents exist.

	controllerutil.RemoveFinalizer(cp, CredentialProviderFinalizer)
	if err := r.Update(ctx, cp); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *CredentialProviderReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.CredentialProvider{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}
