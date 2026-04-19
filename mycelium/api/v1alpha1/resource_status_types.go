package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BaseMyceliumResourceStatus holds the common status fields shared by all top-level resource types.
type BaseMyceliumResourceStatus struct {
	// ObservedGeneration is the generation of the spec last reconciled by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// Conditions represent the latest observations of this resource.
	// Known condition types: "Ready"
	// +optional
	// +listType=map
	// +listMapKey=type
	// +kubebuilder:validation:MaxItems=8
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// ReferencedResourceStatus tracks a single managed sub-resource and its conditions.
type ReferencedResourceStatus struct {
	// ResourceRef is a reference to the observed resource.
	// +optional
	ResourceRef *corev1.TypedObjectReference `json:"ref,omitempty"`
	// Conditions represent the latest observations of this sub-resource.
	// +optional
	// +listType=map
	// +listMapKey=type
	// +kubebuilder:validation:MaxItems=8
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}
