// pool-operator/api/v1alpha1/types.go
package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Desired",type=integer,JSONPath=`.spec.desired`
// +kubebuilder:printcolumn:name="Available",type=integer,JSONPath=`.status.available`
// +kubebuilder:printcolumn:name="Claimed",type=integer,JSONPath=`.status.claimed`
// +kubebuilder:printcolumn:name="Warming",type=integer,JSONPath=`.status.warming`

type ExecutorPool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ExecutorPoolSpec   `json:"spec,omitempty"`
	Status ExecutorPoolStatus `json:"status,omitempty"`
}

type ExecutorPoolSpec struct {
	// Desired number of pods that are available or warming up.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Required
	Desired int32 `json:"desired"`

	// LeaseTTL is how long a claim is valid without renewal.
	// Executor must call /renew within this window.
	// +kubebuilder:default="30s"
	// +optional
	LeaseTTL metav1.Duration `json:"leaseTTL,omitempty"`

	// WarmingTimeout is how long a pod can stay in warming state
	// before being considered stuck and deleted.
	// +kubebuilder:default="5m"
	// +optional
	WarmingTimeout metav1.Duration `json:"warmingTimeout,omitempty"`

	// MaxSurge is the maximum number of pods to create per reconcile cycle.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=10
	// +optional
	MaxSurge int32 `json:"maxSurge,omitempty"`

	// PodTemplate defines the pod spec for executor pods in this pool.
	// +kubebuilder:validation:Required
	PodTemplate corev1.PodTemplateSpec `json:"podTemplate"`
}

type ExecutorPoolStatus struct {
	// Available is the number of pods ready to be claimed.
	Available int32 `json:"available,omitempty"`

	// Claimed is the number of pods currently claimed by executors.
	Claimed int32 `json:"claimed,omitempty"`

	// Warming is the number of pods that are starting up.
	Warming int32 `json:"warming,omitempty"`

	// Conditions represent the latest available observations.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true

type ExecutorPoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ExecutorPool `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ExecutorPool{}, &ExecutorPoolList{})
}
