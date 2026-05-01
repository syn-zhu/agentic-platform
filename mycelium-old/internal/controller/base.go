package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"mycelium.io/mycelium/pkg/wellknown"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// helper is the minimal surface area the phase functions need from the
// reconciler. *Base satisfies it automatically.
type helper interface {
	apply(ctx context.Context, owner metav1.Object, obj client.Object) (*corev1.TypedLocalObjectReference, error)
	List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error
}

// Base embeds the controller-runtime client and scheme, and provides shared
// helpers used across all mycelium reconcilers.
type Base struct {
	client.Client
	Scheme *runtime.Scheme
}

// apply stamps TypeMeta on obj (derived from the scheme), sets owner as the
// controller owner reference, injects the managed-by label, then applies obj
// via SSA. It returns a TypedLocalObjectReference for the applied object so
// callers can build a ReferenceStatus without re-deriving the GVK.
func (b *Base) apply(ctx context.Context, owner metav1.Object, obj client.Object) (*corev1.TypedLocalObjectReference, error) {
	gvk, err := apiutil.GVKForObject(obj, b.Scheme)
	if err != nil {
		return nil, fmt.Errorf("resolving GVK: %w", err)
	}
	obj.GetObjectKind().SetGroupVersionKind(gvk)

	if err := controllerutil.SetControllerReference(owner, obj, b.Scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference: %w", err)
	}

	labels := obj.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	labels["app.kubernetes.io/managed-by"] = wellknown.MyceliumControllerName
	obj.SetLabels(labels)

	if err := b.Patch(ctx, obj, client.Apply, client.FieldOwner(wellknown.MyceliumControllerName), client.ForceOwnership); err != nil {
		return nil, err
	}

	return &corev1.TypedLocalObjectReference{
		APIGroup: &gvk.Group,
		Kind:     gvk.Kind,
		Name:     obj.GetName(),
	}, nil
}
