package controller

import (
	"context"
	"fmt"
	"time"

	v1alpha1 "github.com/mongodb/mycelium/api/v1alpha1"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
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

	// Check for dependent Tools before allowing deletion
	dependents, err := r.findDependentTools(ctx, cp)
	if err != nil {
		return ctrl.Result{}, err
	}

	if len(dependents) > 0 {
		logger.Info("CredentialProvider has dependent Tools, blocking deletion",
			"name", cp.Name, "dependents", dependents)
		meta.SetStatusCondition(&cp.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "DeletionBlocked",
			Message:            fmt.Sprintf("Cannot delete: referenced by Tools: %v", dependents),
			LastTransitionTime: metav1.Now(),
		})
		_ = r.Status().Update(ctx, cp)
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	logger.Info("Cleaning up CredentialProvider", "name", cp.Name)

	controllerutil.RemoveFinalizer(cp, CredentialProviderFinalizer)
	if err := r.Update(ctx, cp); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// findDependentTools lists all Tools in the same namespace that reference this CredentialProvider.
func (r *CredentialProviderReconciler) findDependentTools(ctx context.Context, cp *v1alpha1.CredentialProvider) ([]string, error) {
	var toolList v1alpha1.ToolList
	if err := r.List(ctx, &toolList, client.InNamespace(cp.Namespace)); err != nil {
		return nil, err
	}

	var dependents []string
	for _, tool := range toolList.Items {
		if tool.Spec.Credentials == nil {
			continue
		}
		// Check OAuth credential ref
		if tool.Spec.Credentials.OAuth != nil && tool.Spec.Credentials.OAuth.ProviderRef.Name == cp.Name {
			dependents = append(dependents, tool.Name)
			continue
		}
		// Check API key credential refs
		for _, apiKey := range tool.Spec.Credentials.APIKeys {
			if apiKey.ProviderRef.Name == cp.Name {
				dependents = append(dependents, tool.Name)
				break
			}
		}
	}
	return dependents, nil
}

func (r *CredentialProviderReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.CredentialProvider{}).
		Complete(r)
}
