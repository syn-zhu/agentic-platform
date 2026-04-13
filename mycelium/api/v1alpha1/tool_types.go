package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// OAuthCredentialRef binds a tool to an OAuth CredentialProvider with specific scopes.
type OAuthCredentialRef struct {
	// ProviderRef references an OAuth CredentialProvider in the same namespace.
	// +kubebuilder:validation:Required
	ProviderRef corev1.LocalObjectReference `json:"providerRef"`
	// Scopes are the OAuth scopes required by this tool.
	// +required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=32
	// +kubebuilder:validation:XValidation:rule="self.all(s, size(s) >= 1 && size(s) <= 256)",message="each scope must be 1-256 characters"
	Scopes []string `json:"scopes"`
}

// APIKeyCredentialRef binds a tool to an API key CredentialProvider.
type APIKeyCredentialRef struct {
	// ProviderRef references an API key CredentialProvider in the same namespace.
	// +kubebuilder:validation:Required
	ProviderRef corev1.LocalObjectReference `json:"providerRef"`
}

// ToolCredentials defines the credential providers required by a tool.
type ToolCredentials struct {
	// OAuth is the optional OAuth credential binding. At most one per tool,
	// since each requires a user authorization flow.
	// +optional
	OAuth *OAuthCredentialRef `json:"oauth,omitempty"`
	// APIKeys are optional API key credential bindings. Multiple allowed,
	// since API keys don't require user authorization.
	// +optional
	// +kubebuilder:validation:MaxItems=8
	APIKeys []APIKeyCredentialRef `json:"apiKeys,omitempty"`
}

// ToolContainer defines the container spec for the tool executor.
type ToolContainer struct {
	// Image is the container image for the tool executor.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	Image string `json:"image"`
}

// ToolScaling defines scaling parameters for the tool executor.
// +kubebuilder:validation:XValidation:rule="!has(self.minScale) || !has(self.maxScale) || self.minScale <= self.maxScale",message="minScale must be less than or equal to maxScale"
type ToolScaling struct {
	// MinScale is the minimum number of replicas (0 for scale-to-zero).
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	// +kubebuilder:default=0
	// +optional
	MinScale *int32 `json:"minScale,omitempty"`
	// MaxScale is the maximum number of replicas.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	// +kubebuilder:default=10
	// +optional
	MaxScale *int32 `json:"maxScale,omitempty"`
}

// ToolSpec defines the desired state of Tool.
// The MCP tool name is derived from the resource's metadata.name by converting
// hyphens to underscores (e.g., metadata.name "list-repos" → MCP name "list_repos").
// The Mycelium API layer performs the reverse conversion when creating resources
// from user-provided tool names.
type ToolSpec struct {
	// Description is the human-readable tool description.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	Description string `json:"description"`
	// Credentials defines the credential providers required by this tool.
	// +optional
	Credentials *ToolCredentials `json:"credentials,omitempty"`
	// InputSchema is the MCP-compatible JSON Schema for the tool's input.
	// +optional
	InputSchema *apiextv1.JSON `json:"inputSchema,omitempty"`
	// Container defines the tool executor container.
	// +kubebuilder:validation:Required
	Container ToolContainer `json:"container"`
	// Scaling defines optional scaling overrides for the Knative Service.
	// +optional
	Scaling *ToolScaling `json:"scaling,omitempty"`
}

// ToolStatus defines the observed state of Tool.
type ToolStatus struct {
	// KnativeServiceRef references the generated Knative Service for this tool.
	// The controller sets an ownerReference on the Knative Service pointing back
	// to this Tool, so deleting the Tool cascade-deletes the Service.
	// +optional
	KnativeServiceRef *corev1.LocalObjectReference `json:"knativeServiceRef,omitempty"`
	// Conditions represent the latest observations of the Tool's state.
	// Known condition types: "Ready", "KnativeServiceReady", "CredentialsValid"
	// +optional
	// +listType=map
	// +listMapKey=type
	// +kubebuilder:validation:MaxItems=8
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=tl,categories=mycelium
// +kubebuilder:printcolumn:name="Tool",type=string,JSONPath=".metadata.name",description="Tool resource name (MCP name = hyphens→underscores)"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=='Ready')].status`,description="Whether the tool is ready"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Tool is the Schema for the tools API.
type Tool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +required
	Spec   ToolSpec   `json:"spec"`
	Status ToolStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ToolList contains a list of Tool.
type ToolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Tool `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Tool{}, &ToolList{})
}
