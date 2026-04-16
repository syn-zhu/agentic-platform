package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CredentialProviderRef references a CredentialProvider by name in the same namespace.
type CredentialProviderRef struct {
	// Name is the name of the CredentialProvider.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// CredentialBinding binds a tool to a credential provider.
// The Type field uses the same CredentialProviderType enum as the CredentialProvider CRD.
//
// +kubebuilder:validation:XValidation:message="oauth must be set when type is OAuth",rule="self.type == 'OAuth' ? has(self.oauth) : !has(self.oauth)"
// +kubebuilder:validation:XValidation:message="apiKey must be set when type is APIKey",rule="self.type == 'APIKey' ? has(self.apiKey) : !has(self.apiKey)"
type CredentialBinding struct {
	// Type is the type of credential binding (must match the referenced CredentialProvider's type).
	// +unionDiscriminator
	// +kubebuilder:validation:Required
	Type CredentialProviderType `json:"type"`
	// OAuth binds this tool to an OAuth CredentialProvider with specific scopes.
	// +optional
	OAuth *OAuthCredentialBinding `json:"oauth,omitempty"`
	// APIKey binds this tool to an API key CredentialProvider.
	// +optional
	APIKey *APIKeyCredentialBinding `json:"apiKey,omitempty"`
}

// OAuthCredentialBinding binds a tool to an OAuth CredentialProvider with specific scopes.
type OAuthCredentialBinding struct {
	// CredentialProviderRef references an OAuth CredentialProvider in the same namespace.
	// +kubebuilder:validation:Required
	CredentialProviderRef CredentialProviderRef `json:"credentialProviderRef"`
	// Scopes are the OAuth scopes required by this tool.
	// +required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=32
	// +kubebuilder:validation:XValidation:rule="self.all(s, size(s) >= 1 && size(s) <= 256)",message="each scope must be 1-256 characters"
	Scopes []string `json:"scopes"`
}

// APIKeyCredentialBinding binds a tool to an API key CredentialProvider.
type APIKeyCredentialBinding struct {
	// CredentialProviderRef references an API key CredentialProvider in the same namespace.
	// +kubebuilder:validation:Required
	CredentialProviderRef CredentialProviderRef `json:"credentialProviderRef"`
}

// WorkerPoolConfig defines the container and scaling settings for tool executor pods.
// +kubebuilder:validation:XValidation:rule="!has(self.minReplicas) || !has(self.maxReplicas) || self.minReplicas <= self.maxReplicas",message="minReplicas must be less than or equal to maxReplicas"
type WorkerPoolConfig struct {
	// Image is the container image for the tool executor.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	Image string `json:"image"`
	// MinReplicas is the minimum number of replicas (0 for scale-to-zero).
	// If not set, the platform default is used.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	// +optional
	MinReplicas *int32 `json:"minReplicas,omitempty"`
	// MaxReplicas is the maximum number of replicas.
	// If not set, the platform default is used.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	// +optional
	MaxReplicas *int32 `json:"maxReplicas,omitempty"`
}

// CredentialProviderName returns the name of the referenced CredentialProvider.
func (cb *CredentialBinding) CredentialProviderName() string {
	switch cb.Type {
	case CredentialProviderTypeOAuth:
		return cb.OAuth.CredentialProviderRef.Name
	case CredentialProviderTypeAPIKey:
		return cb.APIKey.CredentialProviderRef.Name
	default:
		return ""
	}
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
	// CredentialBindings are the credential provider bindings required by this tool.
	// At most one OAuth credential binding is allowed (since each requires a user
	// authorization flow). Multiple API key bindings are allowed.
	// +optional
	// +kubebuilder:validation:MaxItems=9
	// +kubebuilder:validation:XValidation:rule="self.filter(c, has(c.oauth)).size() <= 1",message="at most one OAuth credential binding is allowed per tool"
	// +kubebuilder:validation:XValidation:rule="self.map(b, b.type == 'OAuth' ? b.oauth.credentialProviderRef.name : b.apiKey.credentialProviderRef.name).distinct().size() == self.size()",message="each credential provider may only be referenced once per tool"
	CredentialBindings []CredentialBinding `json:"credentialBindings,omitempty"`
	// InputSchema is the MCP-compatible JSON Schema for the tool's input.
	// +optional
	InputSchema *apiextv1.JSON `json:"inputSchema,omitempty"`
	// WorkerPool defines the container image and scaling settings for the tool executor.
	// +kubebuilder:validation:Required
	WorkerPool WorkerPoolConfig `json:"workerPool"`
}

// ToolStatus defines the observed state of Tool.
type ToolStatus struct {
	BaseStatus `json:",inline"`
	// Service tracks the generated Knative Service (owned resource).
	// +optional
	Service *corev1.TypedLocalObjectReference `json:"service,omitempty"`
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
