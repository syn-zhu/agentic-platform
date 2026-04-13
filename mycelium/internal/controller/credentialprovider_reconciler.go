package controller

import (
	"context"
	"fmt"
	"time"

	v1alpha1 "github.com/mongodb/mycelium/api/v1alpha1"

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

	// Validate parent Project
	if err := r.reconcileProject(ctx, &cp); err != nil {
		_ = r.Status().Update(ctx, &cp)
		return ctrl.Result{}, err
	}

	// Validate referenced Secret
	if err := r.reconcileSecret(ctx, &cp); err != nil {
		r.setReadyCondition(&cp)
		_ = r.Status().Update(ctx, &cp)
		return ctrl.Result{}, err
	}

	// Set rollup Ready condition and persist status
	r.setReadyCondition(&cp)
	if err := r.Status().Update(ctx, &cp); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// reconcileProject checks that the parent Project exists.
// Returns error only for transient API failures.
func (r *CredentialProviderReconciler) reconcileProject(ctx context.Context, cp *v1alpha1.CredentialProvider) error {
	var proj v1alpha1.Project
	if err := r.Get(ctx, types.NamespacedName{Name: cp.Namespace}, &proj); err != nil {
		if errors.IsNotFound(err) {
			meta.SetStatusCondition(&cp.Status.Conditions, metav1.Condition{
				Type:               "ProjectValid",
				Status:             metav1.ConditionFalse,
				Reason:             "ProjectNotFound",
				Message:            fmt.Sprintf("Project %s not found", cp.Namespace),
				LastTransitionTime: metav1.Now(),
			})
			return nil
		}
		return fmt.Errorf("checking Project: %w", err)
	}

	meta.SetStatusCondition(&cp.Status.Conditions, metav1.Condition{
		Type:               "ProjectValid",
		Status:             metav1.ConditionTrue,
		Reason:             "Valid",
		Message:            "Parent project exists",
		LastTransitionTime: metav1.Now(),
	})
	return nil
}

// reconcileSecret validates the referenced K8s Secret exists.
// Returns error only for transient API failures.
func (r *CredentialProviderReconciler) reconcileSecret(ctx context.Context, cp *v1alpha1.CredentialProvider) error {
	var secretName string
	if cp.IsOAuth() {
		secretName = cp.Spec.OAuth.ClientSecretRef.Name
	} else {
		secretName = cp.Spec.APIKey.APIKeySecretRef.Name
	}

	var secret corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: cp.Namespace}, &secret); err != nil {
		if errors.IsNotFound(err) {
			meta.SetStatusCondition(&cp.Status.Conditions, metav1.Condition{
				Type:               "SecretValid",
				Status:             metav1.ConditionFalse,
				Reason:             "SecretNotFound",
				Message:            fmt.Sprintf("Secret %s not found", secretName),
				LastTransitionTime: metav1.Now(),
			})
			return nil
		}
		return fmt.Errorf("checking Secret %s: %w", secretName, err)
	}

	meta.SetStatusCondition(&cp.Status.Conditions, metav1.Condition{
		Type:               "SecretValid",
		Status:             metav1.ConditionTrue,
		Reason:             "Valid",
		Message:            fmt.Sprintf("Secret %s exists", secretName),
		LastTransitionTime: metav1.Now(),
	})
	return nil
}

// setReadyCondition computes the rollup Ready condition from sub-conditions.
func (r *CredentialProviderReconciler) setReadyCondition(cp *v1alpha1.CredentialProvider) {
	projValid := meta.IsStatusConditionTrue(cp.Status.Conditions, "ProjectValid")
	secretValid := meta.IsStatusConditionTrue(cp.Status.Conditions, "SecretValid")

	if projValid && secretValid {
		meta.SetStatusCondition(&cp.Status.Conditions, metav1.Condition{
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
		default:
			reason = "SecretMissing"
		}
		meta.SetStatusCondition(&cp.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             reason,
			Message:            "One or more sub-conditions not satisfied",
			LastTransitionTime: metav1.Now(),
		})
	}
}

func (r *CredentialProviderReconciler) reconcileDelete(ctx context.Context, cp *v1alpha1.CredentialProvider) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Cleaning up CredentialProvider", "name", cp.Name)

	// Wait for dependent Tools to be removed before finalizing.
	var tools v1alpha1.ToolList
	if err := r.List(ctx, &tools, client.InNamespace(cp.Namespace),
		client.MatchingFields{IndexToolCredentialBindings: cp.Name}); err != nil {
		return ctrl.Result{}, err
	}
	if len(tools.Items) > 0 {
		logger.Info("CredentialProvider still has dependent Tools, requeuing",
			"tools", len(tools.Items))
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	controllerutil.RemoveFinalizer(cp, CredentialProviderFinalizer)
	if err := r.Update(ctx, cp); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// findCPsForProject maps a Project event to all CredentialProviders in that namespace.
func (r *CredentialProviderReconciler) findCPsForProject(ctx context.Context, obj client.Object) []ctrl.Request {
	var cpList v1alpha1.CredentialProviderList
	if err := r.List(ctx, &cpList, client.InNamespace(obj.GetName())); err != nil {
		return nil
	}
	requests := make([]ctrl.Request, 0, len(cpList.Items))
	for _, cp := range cpList.Items {
		requests = append(requests, ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      cp.Name,
				Namespace: cp.Namespace,
			},
		})
	}
	return requests
}

func (r *CredentialProviderReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.CredentialProvider{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&v1alpha1.Project{},
			handler.EnqueueRequestsFromMapFunc(r.findCPsForProject),
		).
		Complete(r)
}
