package v1alpha1

import (
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ResourceRef is a typed reference to a Kubernetes resource.
type ResourceRef struct {
	// Group is the API group of the referenced resource.
	Group string `json:"group"`
	// Kind is the kind of the referenced resource.
	Kind string `json:"kind"`
	// Name is the name of the referenced resource.
	Name string `json:"name"`
}

// ResourceBinding binds a tool to an OAuthResource with specific scopes.
type ResourceBinding struct {
	// Ref is a typed reference to the OAuthResource.
	Ref ResourceRef `json:"ref"`
	// Scopes are the OAuth scopes required by this tool.
	Scopes []string `json:"scopes"`
}

// ToolContainer defines the container spec for the tool executor.
type ToolContainer struct {
	// Image is the container image for the tool executor.
	Image string `json:"image"`
}

// ToolScaling defines scaling parameters for the tool executor.
type ToolScaling struct {
	// MinScale is the minimum number of replicas (0 for scale-to-zero).
	// +kubebuilder:default=0
	// +optional
	MinScale *int32 `json:"minScale,omitempty"`
	// MaxScale is the maximum number of replicas.
	// +kubebuilder:default=10
	// +optional
	MaxScale *int32 `json:"maxScale,omitempty"`
}

// ToolConfigSpec defines the desired state of ToolConfig.
type ToolConfigSpec struct {
	// ToolName is the MCP tool name exposed to agents.
	ToolName string `json:"toolName"`
	// Description is the human-readable tool description.
	Description string `json:"description"`
	// Resource is the optional OAuth resource binding.
	// +optional
	Resource *ResourceBinding `json:"resource,omitempty"`
	// InputSchema is the MCP-compatible JSON Schema for the tool's input.
	// +optional
	InputSchema *apiextv1.JSON `json:"inputSchema,omitempty"`
	// Container defines the tool executor container.
	Container ToolContainer `json:"container"`
	// Scaling defines optional scaling overrides.
	// +optional
	Scaling *ToolScaling `json:"scaling,omitempty"`
}

// ToolConfigStatus defines the observed state of ToolConfig.
type ToolConfigStatus struct {
	// KnativeServiceURL is the internal URL of the generated Knative Service.
	// +optional
	KnativeServiceURL string `json:"knativeServiceUrl,omitempty"`
	// Conditions represent the latest observations.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced

// ToolConfig is the Schema for the toolconfigs API.
type ToolConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ToolConfigSpec   `json:"spec,omitempty"`
	Status ToolConfigStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ToolConfigList contains a list of ToolConfig.
type ToolConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ToolConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ToolConfig{}, &ToolConfigList{})
}
