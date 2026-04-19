package krtcontroller

import (
	"context"
	"fmt"

	"istio.io/istio/pkg/kube/controllers"
	"istio.io/istio/pkg/kube/krt"
	"k8s.io/apimachinery/pkg/types"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "mycelium.io/mycelium/api/v1alpha1"
	"mycelium.io/mycelium/pkg/wellknown"
)

const ecosystemNamespaceFieldManager = "mycelium-ecosystem-namespace"

// EcosystemNamespace is an in-memory KRT type representing the desired
// namespace for a MyceliumEcosystem. It is never written as a CRD; it is
// the intermediate representation that feeds the namespace sink.
type EcosystemNamespace struct {
	// EcosystemName is both the name of the owning ecosystem and the name
	// of the namespace to create — they are always identical.
	EcosystemName string
	EcosystemUID  types.UID
}

// ResourceName satisfies krt.ResourceNamer. Used as the KRT primary key.
// Value receiver is required: NewCollection stores O values (not *O), so the
// KRT key resolver sees EcosystemNamespace, not *EcosystemNamespace.
func (n EcosystemNamespace) ResourceName() string { return n.EcosystemName }

// ecosystemNamespaceCollection derives the desired EcosystemNamespace for
// every MyceliumEcosystem. This is a 1:1 derivation — one ecosystem yields
// exactly one desired namespace.
//
// krt.NewCollection[I,O] returns Collection[O]; the transformation returns *O
// (nil = no output). So the collection element type is EcosystemNamespace, not
// *EcosystemNamespace.
func ecosystemNamespaceCollection(
	ecosystems krt.Collection[*v1alpha1.MyceliumEcosystem],
) krt.Collection[EcosystemNamespace] {
	return krt.NewCollection(
		ecosystems,
		func(_ krt.HandlerContext, eco *v1alpha1.MyceliumEcosystem) *EcosystemNamespace {
			// Exclude deleting ecosystems. Returning nil removes this entry from
			// the derived collection, which the sink observes as a delete event.
			if eco.DeletionTimestamp != nil {
				return nil
			}
			return &EcosystemNamespace{
				EcosystemName: eco.Name,
				EcosystemUID:  eco.UID,
			}
		},
		krt.WithName("EcosystemNamespaces"),
	)
}

// registerNamespaceSink is the output sink for the namespace pipeline.
// It is registered on the *derived* EcosystemNamespace collection, not on
// the root ecosystem informer. On delete, the OwnerReference on the
// Namespace causes Kubernetes GC to clean up automatically.
//
// RegisterBatch is preferred over Register for sinks: it processes events in
// batches and, with runExistingState=true, replays any already-synced entries
// as EventAdd on registration so the initial reconcile happens automatically.
func registerNamespaceSink(
	ctx context.Context,
	ssaClient client.Client,
	namespaces krt.Collection[EcosystemNamespace],
) {
	namespaces.RegisterBatch(func(events []krt.Event[EcosystemNamespace]) {
		for _, e := range events {
			switch e.Event {
			case controllers.EventDelete:
				// Namespace is garbage-collected by Kubernetes via the
				// OwnerReference we set on creation — no explicit delete needed.
				continue
			case controllers.EventAdd, controllers.EventUpdate:
				ns := e.Latest()
				if err := applyEcosystemNamespace(ctx, ssaClient, &ns); err != nil {
					// TODO: surface via ecosystem status condition.
					fmt.Printf("%s: failed to apply namespace for ecosystem %q: %v\n",
						ecosystemNamespaceFieldManager, ns.EcosystemName, err)
				}
			}
		}
	}, true /* runExistingState */)
}

// applyEcosystemNamespace SSA-applies the Namespace for the given desired state.
func applyEcosystemNamespace(
	ctx context.Context,
	c client.Client,
	ns *EcosystemNamespace,
) error {
	// TODO: extract owner reference creation into a helper function
	ownerRef := metav1ac.OwnerReference().
		WithAPIVersion(v1alpha1.MyceliumEcosystemGVK.GroupVersion().String()).
		WithKind(v1alpha1.MyceliumEcosystemGVK.Kind).
		WithName(ns.EcosystemName).
		WithUID(ns.EcosystemUID).
		WithController(true).
		WithBlockOwnerDeletion(true)

	desired := corev1ac.Namespace(ns.EcosystemName).
		WithOwnerReferences(ownerRef).
		WithLabels(map[string]string{
			"mycelium.io/ecosystem":        ns.EcosystemName,
			"app.kubernetes.io/managed-by": wellknown.DefaultMyceliumControllerName,
		})

	return c.Apply(ctx, desired,
		client.FieldOwner(ecosystemNamespaceFieldManager),
		client.ForceOwnership,
	)
}
