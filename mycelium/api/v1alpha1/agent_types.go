package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ToolRef is a reference to a Tool in the same namespace.
type ToolRef struct {
	// Ref references a Tool by name in the same namespace.
	// +kubebuilder:validation:Required
	Ref corev1.LocalObjectReference `json:"ref"`
}

// AgentContainer defines the container spec for the agent sandbox.
type AgentContainer struct {
	// Image is the container image for the agent.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	Image string `json:"image"`
}

// WarmPoolConfig defines the warm pool settings for agent sandboxes.
type WarmPoolConfig struct {
	// Replicas is the number of pre-warmed sandbox pods to maintain.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	// +kubebuilder:default=1
	Replicas int32 `json:"replicas"`
}

// SandboxConfig defines the sandbox lifecycle settings.
type SandboxConfig struct {
	// ShutdownTimeout is the duration after which an idle sandbox is released.
	// Format: Go duration string (e.g., "30m", "1h").
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=32
	// +kubebuilder:validation:Pattern=`^[0-9]+(s|m|h)$`
	ShutdownTimeout string `json:"shutdownTimeout"`
	// WarmPool configures pre-warmed sandbox pods for this agent.
	// +optional
	WarmPool *WarmPoolConfig `json:"warmPool,omitempty"`
}

// AgentSpec defines the desired state of Agent.
type AgentSpec struct {
	// Description is the human-readable agent description.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	Description string `json:"description"`
	// Tools are the tools this agent can access, as typed references to Tool resources
	// in the same namespace. The Mycelium controller uses this to generate the
	// AGW tool-access policy CEL expressions.
	// +required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	Tools []ToolRef `json:"tools"`
	// Container defines the agent sandbox container.
	// +kubebuilder:validation:Required
	Container AgentContainer `json:"container"`
	// Sandbox configures the agent sandbox lifecycle. If nil, defaults are used.
	// +optional
	Sandbox *SandboxConfig `json:"sandbox,omitempty"`
}

// AgentStatus defines the observed state of Agent.
type AgentStatus struct {
	// ServiceAccountRef references the K8s ServiceAccount for this agent.
	// Created by the controller, used in tool-access policy CEL expressions
	// for identity resolution.
	// +optional
	ServiceAccountRef *corev1.LocalObjectReference `json:"serviceAccountRef,omitempty"`
	// WarmPoolRef references the generated SandboxWarmPool for this agent.
	// +optional
	WarmPoolRef *corev1.LocalObjectReference `json:"warmPoolRef,omitempty"`
	// Conditions represent the latest observations of the Agent's state.
	// Known condition types: "Ready", "ToolsValid"
	// +optional
	// +listType=map
	// +listMapKey=type
	// +kubebuilder:validation:MaxItems=8
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=ag,categories=mycelium
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=='Ready')].status`,description="Whether the agent is ready"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Agent is the Schema for the agents API. Each Agent defines which Tools it
// can access and how its sandbox is configured.
type Agent struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +required
	Spec   AgentSpec   `json:"spec"`
	Status AgentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AgentList contains a list of Agent.
type AgentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Agent `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Agent{}, &AgentList{})
}
