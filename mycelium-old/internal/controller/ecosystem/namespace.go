package ecosystem

import (
	"context"
	"fmt"

	"github.com/docker/docker/daemon/logger"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	"mycelium.io/mycelium/api/v1alpha1"
	"mycelium.io/mycelium/pkg/wellknown"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type NamespaceReconciler struct {
	client.Client
}

const (
	ecosystemNamespaceManagerName          = "mycelium-ecosystem-namespace"
	ecosystemNamespaceStatusManagerName    = "mycelium-ecosystem-namespace-status"
	ecosystemNamespaceFinalizerManagerName = "mycelium-ecosystem-namespace-finalizer"
	ecosystemNamespaceFinalizer            = "mycelium.io/ecosystem-namespace-finalizer"
)

func (r *NamespaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named(ecosystemNamespaceManagerName).
		// TODO: it should basically enqueue only when something relevant to the namespace changes
		// it writes specifically the namespace ref and status field
		For(&v1alpha1.MyceliumEcosystem{}, builder.WithPredicates(predicate.Funcs{
			UpdateFunc: func(e event.UpdateEvent) bool {
				oldEcosystem := e.ObjectOld.(*v1alpha1.MyceliumEcosystem)
				newEcosystem := e.ObjectNew.(*v1alpha1.MyceliumEcosystem)

				if oldEcosystem.Generation != newEcosystem.Generation {
					return true
				}

				if !oldEcosystem.HasReadyCondition() && newEcosystem.HasReadyCondition() {
					return true
				}

				return false
			},
			CreateFunc: func(e event.CreateEvent) bool {
				ecosystem := e.Object.(*v1alpha1.MyceliumEcosystem)
				return ecosystem.HasReadyCondition() || !ecosystem.DeletionTimestamp.IsZero()
			},
		})).
		Owns(&corev1.Namespace{}, builder.WithPredicates(predicate.Funcs{
			UpdateFunc: func(e event.UpdateEvent) bool {
				oldNamespace := e.ObjectOld.(*corev1.Namespace)
				newNamespace := e.ObjectNew.(*corev1.Namespace)

				if oldNamespace.Generation != newNamespace.Generation {
					return true
				}

				return !equality.Semantic.DeepEqual(oldNamespace.Status.Phase, newNamespace.Status.Phase)
			},
		})).
		Complete(r)

	// 		var cnpNeedsReconcile = predicate.TypedFuncs[*ciliumv2.CiliumNetworkPolicy]{
	//     UpdateFunc: func(e event.TypedUpdateEvent[*ciliumv2.CiliumNetworkPolicy]) bool {
	//         // Skip informer resyncs where nothing on the server changed.
	//         if e.ObjectOld.ResourceVersion == e.ObjectNew.ResourceVersion {
	//             return false
	//         }

	//         // (a) Drift: fields I own changed. Extract my owned slice from old and new.
	//         oldOwned, err1 := ciliumv2ac.ExtractCiliumNetworkPolicy(e.ObjectOld, "sandbox-network")
	//         newOwned, err2 := ciliumv2ac.ExtractCiliumNetworkPolicy(e.ObjectNew, "sandbox-network")
	//         if err1 == nil && err2 == nil {
	//             if !apiequality.Semantic.DeepEqual(oldOwned, newOwned) {
	//                 return true
	//             }
	//         }

	//         // (b) Observation: the specific status fields I care about changed.
	//         return cnpEnforcementStateChanged(e.ObjectOld, e.ObjectNew)
	//     },
	//     CreateFunc: func(e event.TypedCreateEvent[*ciliumv2.CiliumNetworkPolicy]) bool {
	//         return true // startup replay — always admit
	//     },
	//     DeleteFunc: func(e event.TypedDeleteEvent[*ciliumv2.CiliumNetworkPolicy]) bool {
	//         return true // recreate on drift
	//     },
	// }

	//     UpdateFunc: func(e event.TypedUpdateEvent[*magentav1.SandboxClaim]) bool {
	//     if e.ObjectOld.ResourceVersion == e.ObjectNew.ResourceVersion {
	//         return false
	//     }
	//     old, new := e.ObjectOld, e.ObjectNew

	//     // Spec changes always matter.
	//     if old.Generation != new.Generation {
	//         return true
	//     }
	//     // PodRef appeared or changed.
	//     if !reflect.DeepEqual(old.Status.PodRef, new.Status.PodRef) {
	//         return true
	//     }
	//     // Deletion kicked off.
	//     if old.DeletionTimestamp.IsZero() && !new.DeletionTimestamp.IsZero() {
	//         return true
	//     }
	//     // Everything else on status — including conditions I might have written — is ignored.
	//     return false
	// },
	// CreateFunc: func(e event.TypedCreateEvent[*magentav1.SandboxClaim]) bool {
	//     sc := e.Object
	//     return sc.Status.PodRef != nil || !sc.DeletionTimestamp.IsZero()
	// },

	// Watches(&ciliumv2.CiliumNetworkPolicy{},
	//     handler.EnqueueRequestsFromMapFunc(r.policyToClaim)).
	// Complete(r)

	// builder.WithPredicates(predicate.NewPredicateFuncs(func(object client.Object) bool {
	// ecosystem, ok := object.(*v1alpha1.MyceliumEcosystem)
	// if !ok {
	// 	return false
	// }

}

func (r *NamespaceReconciler) findOwnedNamespace(ctx context.Context, ecosystem *v1alpha1.MyceliumEcosystem) (*corev1.Namespace, error) {
	var namespace corev1.Namespace
	if err := r.Get(ctx, client.ObjectKey{Name: ecosystem.Name}, &namespace); err != nil {
		return nil, client.IgnoreNotFound(err)
	}

	if metav1.IsControlledBy(&namespace, ecosystem) {
		return &namespace, nil
	}

	return nil, nil
}

func (r *NamespaceReconciler) reconcileStatus(ctx context.Context, ecosystem *v1alpha1.MyceliumEcosystem, namespace *corev1.Namespace) error {
	if namespace == nil {
		// TODO: completely remove the namespace condition

		// WithStatus(magentav1ac.SandboxClaimStatus().
		// 	WithConditions(networkReadyCondition), // still owned
		// // No .WithAssignedIP(...) — releases the IP field.
		// )
		return nil
	}

	switch namespace.Status.Phase {
	case corev1.NamespaceActive:
		// TODO
	case corev1.NamespaceTerminating:
		// TODO: set status to TERMINATING
	default:
		// TODO: set to
		// unknown
	}

	// TODO: we can still extract and compare here

	return r.Status().Apply(ctx, statusAC,
		client.FieldOwner(ecosystemNamespaceStatusManagerName),
		client.ForceOwnership,
	)
}

func (r *NamespaceReconciler) reconcileFinalizer(ctx context.Context, ecosystem *v1alpha1.MyceliumEcosystem, namespace *corev1.Namespace) error {
	// TODO: if the ecosystem is being deleted and the namespace does not exist, then finalizer should not exist
	// Otherwise, finalizer should exist

	if !ecosystem.DeletionTimestamp.IsZero() && namespace == nil {

	}

	// again, we can extract. and compare
	return nil
}

func (r *NamespaceReconciler) reconcileNamespace(ctx context.Context, ecosystem *v1alpha1.MyceliumEcosystem, namespace *corev1.Namespace) error {
	// TODO
	if !ecosystem.DeletionTimestamp.IsZero() {
		if namespace != nil {
			logger.Info("Deleting owned namespace")

			return r.Delete(ctx, namespace)

			// TODO: should we requeue? or are we guaranteed that the status update will be observed?
			// does deleetefunc trigger on deletetimestamp or on actual deletion of the object?
		}
	}

	ownerRef := metav1ac.OwnerReference().
		WithAPIVersion(wellknown.MyceliumEcosystemGVK.GroupVersion().String()).
		WithKind(wellknown.MyceliumEcosystemGVK.Kind).
		WithName(ecosystem.Name).
		WithUID(ecosystem.UID).
		WithController(true).
		WithBlockOwnerDeletion(true)

	desired := corev1ac.Namespace(ecosystem.Name).
		WithOwnerReferences(ownerRef).
		WithLabels(map[string]string{
			"app.kubernetes.io/managed-by": wellknown.MyceliumControllerName,
		}).
		WithAnnotations(map[string]string{
			"mycelium.io/ecosystem-name": ecosystem.Name,
		})

	// TODO: what happens here if namespace is nil?
	owned, err := corev1ac.ExtractNamespace(namespace, ecosystemNamespaceManagerName)
	if err != nil {
		// TODO: handle the case where we failed to extract the owned namespace
	}

	if !equality.Semantic.DeepEqual(owned, desired) {
		return r.Apply(ctx, desired,
			client.FieldOwner(ecosystemNamespaceManagerName),
			client.ForceOwnership,
		)
	}

	return nil
}

func (r *NamespaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, retErr error) {
	logger := ctrl.LoggerFrom(ctx)

	var ecosystem v1alpha1.MyceliumEcosystem
	if err := r.Get(ctx, req.NamespacedName, &ecosystem); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	namespace, err := r.findOwnedNamespace(ctx, &ecosystem)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to find owned namespace: %w", err)
	}

	if !ecosystem.DeletionTimestamp.IsZero() {
		if namespace != nil {
			logger.Info("Deleting owned namespace")

			return ctrl.Result{}, r.Delete(ctx, namespace)

			// TODO: should we requeue? or are we guaranteed that the status update will be observed?
			// does deleetefunc trigger on deletetimestamp or on actual deletion of the object?
		}

		// Remove Finalizer from the ecosystem
		// TODO: create applyconfig for only the finalizer field
		// IMPORTANT: note this is for the FINALIZER field

		// TODO: we can use the same Extract trick here to check if finalizer ever existed?
		// if err := r.Status().Apply(ctx, )

		return ctrl.Result{}, nil
	}

	if controllerutil.AddFinalizer(&ecosystem, ecosystemNamespaceFinalizer) {
		// TODO: apply config to persist the finalizer addition
		// this means we need to watch the finalizer as well in our setup
		return ctrl.Result{}, nil
	}

	ownerRef := metav1ac.OwnerReference().
		WithAPIVersion(wellknown.MyceliumEcosystemGVK.GroupVersion().String()).
		WithKind(wellknown.MyceliumEcosystemGVK.Kind).
		WithName(ecosystem.Name).
		WithUID(ecosystem.UID).
		WithController(true).
		WithBlockOwnerDeletion(true)

	desired := corev1ac.Namespace(ecosystem.Name).
		WithOwnerReferences(ownerRef).
		WithLabels(map[string]string{
			"app.kubernetes.io/managed-by": wellknown.MyceliumControllerName,
		}).
		WithAnnotations(map[string]string{
			"mycelium.io/ecosystem-name": ecosystem.Name,
		})

	// TODO: what happens here if namespace is nil?
	owned, err := corev1ac.ExtractNamespace(namespace, ecosystemNamespaceManagerName)
	if err != nil {
		// TODO: handle the case where we failed to extract the owned namespace
	}

	if !equality.Semantic.DeepEqual(owned, desired) {
		return ctrl.Result{}, r.Apply(ctx, desired,
			client.FieldOwner(ecosystemNamespaceManagerName),
			client.ForceOwnership,
		)
	}

	return ctrl.Result{}, nil
}
