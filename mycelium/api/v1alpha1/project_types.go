package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type IdentityProviderConfig struct {
	// Issuer is the OIDC issuer URL.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:Format=uri
	Issuer string `json:"issuer"`
	// Audiences are the allowed JWT audiences.
	// +required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:XValidation:rule="self.all(a, size(a) >= 1 && size(a) <= 256)",message="each audience must be 1-256 characters"
	Audiences []string `json:"audiences"`
	// AllowedClients are the allowed OAuth client IDs.
	// +optional
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:XValidation:rule="self.all(c, size(c) >= 1 && size(c) <= 256)",message="each client ID must be 1-256 characters"
	AllowedClients []string `json:"allowedClients,omitempty"`
	// AllowedScopes are the allowed OAuth scopes.
	// +optional
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:XValidation:rule="self.all(s, size(s) >= 1 && size(s) <= 256)",message="each scope must be 1-256 characters"
	AllowedScopes []string `json:"allowedScopes,omitempty"`
}

// ProjectSpec defines the desired state of Project.
type ProjectSpec struct {
	// UserVerifierURL is the developer-managed endpoint for session binding.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:Format=uri
	UserVerifierURL string `json:"userVerifierUrl"`
	// IdentityProvider configures the tenant's IdP for inbound JWT validation.
	// +kubebuilder:validation:Required
	IdentityProvider IdentityProviderConfig `json:"identityProvider"`
}

// ProjectStatus defines the observed state of Project.
type ProjectStatus struct {
	// Namespace is the name of the namespace created for this project.
	// +optional
	Namespace string `json:"namespace,omitempty"`
	// Conditions represent the latest observations of the Project's state.
	// Known condition types: "Ready", "NamespaceReady"
	// +optional
	// +listType=map
	// +listMapKey=type
	// +kubebuilder:validation:MaxItems=8
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=proj,categories=mycelium
// +kubebuilder:printcolumn:name="Namespace",type=string,JSONPath=".status.namespace",description="The namespace for this project"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=='Ready')].status`,description="Whether the project is ready"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Project is the Schema for the projects API. Each Project is cluster-scoped
// and owns a namespace where Tools and CredentialProviders live.
type Project struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +required
	Spec   ProjectSpec   `json:"spec"`
	Status ProjectStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ProjectList contains a list of Project.
type ProjectList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Project `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Project{}, &ProjectList{})
}
