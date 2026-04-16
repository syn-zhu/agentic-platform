package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BaseStatus holds the common status fields shared by all top-level resource types.
type BaseStatus struct {
	// Conditions represent the latest observations of this resource.
	// Known condition types: "Ready"
	// +optional
	// +listType=map
	// +listMapKey=type
	// +kubebuilder:validation:MaxItems=8
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// ObservedGeneration is the generation of the spec last reconciled by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

func (b *BaseStatus) SetStatusCondition(condition metav1.Condition) {
	meta.SetStatusCondition(&b.Conditions, condition)
}

func (b *BaseStatus) GetConditions() []metav1.Condition           { return b.Conditions }
func (b *BaseStatus) SetConditions(conditions []metav1.Condition) { b.Conditions = conditions }
