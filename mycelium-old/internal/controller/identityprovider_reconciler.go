package controller

import (
	"context"
	"fmt"

	v1alpha1 "mycelium.io/mycelium/api/v1alpha1"
	"mycelium.io/mycelium/internal/generate"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kerrors "k8s.io/apimachinery/pkg/util/errors"
	capicond "sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/patch"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// IdentityProviderReconciler reconciles IdentityProvider resources.
// It provisions the JWKS ExternalName Service for the IdP's JWKS endpoint.
// The service is owned by the IdentityProvider so GC handles cleanup on deletion.
type IdentityProviderReconciler struct {
	*Base
}

// +kubebuilder:rbac:groups=mycelium.io,resources=identityproviders,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=mycelium.io,resources=identityproviders/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete

func (r *IdentityProviderReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, retErr error) {
	var idp v1alpha1.IdentityProvider
	if err := r.Get(ctx, req.NamespacedName, &idp); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	patchHelper, err := patch.NewHelper(&idp, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}

	defer func() {
		if err := patchHelper.Patch(ctx, &idp,
			patch.WithOwnedConditions{Conditions: []string{v1alpha1.EcosystemReadyCondition}},
			patch.WithStatusObservedGeneration{},
		); err != nil {
			retErr = kerrors.NewAggregate([]error{retErr, err})
		}
	}()

	return ctrl.Result{}, r.reconcile(ctx, &idp)
}

func (r *IdentityProviderReconciler) reconcile(ctx context.Context, idp *v1alpha1.IdentityProvider) error {
	objRef, err := r.apply(ctx, idp, generate.JWKSService(idp))
	if err != nil {
		return fmt.Errorf("applying JWKS service for %s: %w", idp.Name, err)
	}
	idp.Status.JWKSService = objRef

	capicond.Set(idp, metav1.Condition{
		Type:   v1alpha1.EcosystemReadyCondition,
		Status: metav1.ConditionTrue,
		Reason: v1alpha1.ProvisionedReason,
	})
	return nil
}

func (r *IdentityProviderReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.IdentityProvider{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Owns(&corev1.Service{}).
		Complete(r)
}
