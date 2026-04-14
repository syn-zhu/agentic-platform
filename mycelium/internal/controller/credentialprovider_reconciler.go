package controller

import (
	"context"
	"fmt"
	"time"

	v1alpha1 "github.com/mongodb/mycelium/api/v1alpha1"
	myceliumutil "github.com/mongodb/mycelium/internal/util"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
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

func (r *CredentialProviderReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, reterr error) {
	logger := log.FromContext(ctx)

	var cp v1alpha1.CredentialProvider
	if err := r.Get(ctx, req.NamespacedName, &cp); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !cp.DeletionTimestamp.IsZero() {
		logger.Info("Cleaning up CredentialProvider", "name", cp.Name)
		return r.reconcileDelete(ctx, &cp)
	}

	if !controllerutil.ContainsFinalizer(&cp, CredentialProviderFinalizer) {
		return r.reconcileCreate(ctx, &cp)
	}

	original := cp.DeepCopy()

	defer func() {
		if !equality.Semantic.DeepEqual(original.Status, cp.Status) {
			if err := r.Status().Patch(ctx, &cp, client.MergeFrom(original)); err != nil {
				reterr = kerrors.NewAggregate([]error{reterr, err})
			}
		}
	}()

	logger.Info("Reconciling CredentialProvider", "name", cp.Name, "isOAuth", cp.IsOAuth(), "isAPIKey", cp.IsAPIKey())

	var errs []error
	res := ctrl.Result{}
	for _, phase := range []func(context.Context, *v1alpha1.CredentialProvider) (ctrl.Result, error){
		r.reconcileProject,
		r.reconcileSecret,
	} {
		phaseResult, err := phase(ctx, &cp)
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

func (r *CredentialProviderReconciler) reconcileProject(ctx context.Context, cp *v1alpha1.CredentialProvider) (ctrl.Result, error) {
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
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("checking Project: %w", err)
	}

	meta.SetStatusCondition(&cp.Status.Conditions, metav1.Condition{
		Type:               "ProjectValid",
		Status:             metav1.ConditionTrue,
		Reason:             "Valid",
		Message:            "Parent project exists",
		LastTransitionTime: metav1.Now(),
	})
	return ctrl.Result{}, nil
}

func (r *CredentialProviderReconciler) reconcileSecret(ctx context.Context, cp *v1alpha1.CredentialProvider) (ctrl.Result, error) {
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
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("checking Secret %s: %w", secretName, err)
	}

	meta.SetStatusCondition(&cp.Status.Conditions, metav1.Condition{
		Type:               "SecretValid",
		Status:             metav1.ConditionTrue,
		Reason:             "Valid",
		Message:            fmt.Sprintf("Secret %s exists", secretName),
		LastTransitionTime: metav1.Now(),
	})
	return ctrl.Result{}, nil
}

func (r *CredentialProviderReconciler) reconcileDelete(ctx context.Context, cp *v1alpha1.CredentialProvider) (ctrl.Result, error) {
	var tools v1alpha1.ToolList
	if err := r.List(ctx, &tools, client.InNamespace(cp.Namespace),
		client.MatchingFields{IndexToolCredentialBindings: cp.Name}); err != nil {
		return ctrl.Result{}, err
	}
	if len(tools.Items) > 0 {
		log.FromContext(ctx).Info("CredentialProvider still has dependent Tools, requeuing", "tools", len(tools.Items))
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	original := cp.DeepCopy()
	controllerutil.RemoveFinalizer(cp, CredentialProviderFinalizer)
	return ctrl.Result{}, r.Client.Patch(ctx, cp, client.MergeFrom(original))
}

func (r *CredentialProviderReconciler) reconcileCreate(ctx context.Context, cp *v1alpha1.CredentialProvider) (ctrl.Result, error) {
	original := cp.DeepCopy()
	controllerutil.AddFinalizer(cp, CredentialProviderFinalizer)
	return ctrl.Result{}, r.Client.Patch(ctx, cp, client.MergeFrom(original))
}

func (r *CredentialProviderReconciler) findCPsForProject(ctx context.Context, obj client.Object) []ctrl.Request {
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
			handler.EnqueueRequestsFromMapFunc(r.findCPsForProject),
		).
		Complete(r)
}
