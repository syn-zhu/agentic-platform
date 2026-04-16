package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ToolRef references a Tool by name in the same namespace.
type ToolRef struct {
	// Name is the name of the Tool.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// ToolBinding binds an agent to a tool. Currently just holds a ToolRef,
// but may be extended with per-binding configuration such as
// per-agent rate limits (see https://agentgateway.dev/docs/kubernetes/latest/mcp/rate-limit/#global-per-tool).
type ToolBinding struct {
	// Tool references a Tool in the same namespace.
	// +kubebuilder:validation:Required
	Tool ToolRef `json:"tool"`
}

// SandboxPoolConfig defines the container, lifecycle, and warm pool settings for agent sandboxes.
type SandboxPoolConfig struct {
	// Image is the container image for the agent.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	Image string `json:"image"`
	// ShutdownTimeout is the duration after which an idle sandbox is released.
	// Format: Go duration string (e.g., "30m", "1h").
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=32
	// +kubebuilder:validation:Pattern=`^[0-9]+(s|m|h)$`
	ShutdownTimeout string `json:"shutdownTimeout"`
	// Replicas is the number of pre-warmed sandbox pods to maintain.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	// +kubebuilder:default=1
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`
}

// AgentSpec defines the desired state of Agent.
type AgentSpec struct {
	// Description is the human-readable agent description.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	Description string `json:"description"`
	// ToolBindings are the tools this agent can access. The Mycelium controller
	// uses this to generate the AGW tool-access policy CEL expressions.
	// +required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	ToolBindings []ToolBinding `json:"toolBindings"`
	// Sandbox defines the container image, lifecycle, and warm pool settings for the agent sandbox.
	// +kubebuilder:validation:Required
	Sandbox SandboxPoolConfig `json:"sandbox"`
}

// AgentStatus defines the observed state of Agent.
type AgentStatus struct {
	BaseStatus `json:",inline"`
	// ServiceAccount tracks the per-agent K8s ServiceAccount (owned resource).
	// +optional
	ServiceAccount *corev1.TypedLocalObjectReference `json:"serviceAccount,omitempty"`
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
