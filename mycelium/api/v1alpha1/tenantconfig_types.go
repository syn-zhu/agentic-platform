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

// TenantConfigSpec defines the desired state of TenantConfig.
type TenantConfigSpec struct {
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

// TenantConfigStatus defines the observed state of TenantConfig.
type TenantConfigStatus struct {
	// Conditions represent the latest observations of the TenantConfig's state.
	// Known condition types: "Ready"
	// +optional
	// +listType=map
	// +listMapKey=type
	// +kubebuilder:validation:MaxItems=8
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=tc,categories=mycelium
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=='Ready')].status`,description="Whether the TenantConfig is ready"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// TenantConfig is the Schema for the tenantconfigs API.
type TenantConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +required
	Spec   TenantConfigSpec   `json:"spec"`
	Status TenantConfigStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// TenantConfigList contains a list of TenantConfig.
type TenantConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TenantConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TenantConfig{}, &TenantConfigList{})
}
