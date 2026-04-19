package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	AgentReadyCondition          = "Ready"
	AgentReadyInitializingReason = "Initializing"
)

// ToolRef references a Tool by name in the same Project.
type ToolRef struct {
	// Name is the name of the Tool.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

type RateLimitConfig struct {
	// Unit is the time unit for the rate limit (e.g., "minute", "hour").
	// +kubebuilder:validation:Required
	Unit string `json:"unit"`
	// RequestsPerUnit is the number of allowed requests per unit of time.
	// +kubebuilder:validation:Minimum=1
	RequestsPerUnit int32 `json:"requestsPerUnit"`
}

// ToolBinding binds an agent to a tool. Currently just holds a ToolRef,
// but may be extended with per-binding configuration such as
// per-agent rate limits (see https://agentgateway.dev/docs/kubernetes/latest/mcp/rate-limit/#global-per-tool).
type ToolBinding struct {
	// ToolRef references a Tool in the same Project.
	// +kubebuilder:validation:Required
	ToolRef `json:"tool"`

	// RateLimit defines per-agent rate limits for this tool binding.
	// +optional
	RateLimit *RateLimitConfig `json:"rateLimit,omitempty"`
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

// MyceliumAgentSpec defines the desired state of Agent.
type MyceliumAgentSpec struct {
	// Description is the human-readable agent description.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	Description string `json:"description"`
	// ToolBindings are the tools this agent can access. The Mycelium controller
	// uses this to generate the AGW tool-access policy CEL expressions.
	// +optional
	ToolBindings []ToolBinding `json:"toolBindings"`
	// Sandbox defines the container image, lifecycle, and warm pool settings for the agent sandbox.
	// +kubebuilder:validation:Required
	SandboxPool SandboxPoolConfig `json:"sandbox"`
}

type ToolBindingStatus struct {
	ToolRef    `json:"toolRef"`
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// MyceliumAgentStatus defines the observed state of Agent.
type MyceliumAgentStatus struct {
	BaseMyceliumResourceStatus `json:",inline"`
	// TODO
	// ToolBindings tracks the resolved ReferenceStatus for each tool binding.
	// +optional
	ToolBindings []ToolBindingStatus `json:"toolBindings,omitempty"`

	ServiceAccount ReferencedResourceStatus
}

// GetConditions and SetConditions implement conditions.Setter so that
// conditions.Set(&agent, cond) stamps ObservedGeneration from agent.GetGeneration()
// onto each condition.
func (a *MyceliumAgent) GetConditions() []metav1.Condition  { return a.Status.Conditions }
func (a *MyceliumAgent) SetConditions(c []metav1.Condition) { a.Status.Conditions = c }

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=ag,categories=mycelium
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=='Ready')].status`,description="Whether the agent is ready"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// MyceliumAgent is the Schema for the agents API. Each MyceliumAgent defines which Tools it
// can access and how its sandbox is configured.
type MyceliumAgent struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +required
	Spec   MyceliumAgentSpec   `json:"spec"`
	Status MyceliumAgentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MyceliumAgentList contains a list of Agent.
type MyceliumAgentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MyceliumAgent `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MyceliumAgent{}, &MyceliumAgentList{})
}
