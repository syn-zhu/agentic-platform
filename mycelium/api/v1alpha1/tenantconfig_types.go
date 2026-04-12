package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type IdentityProviderConfig struct {
	// Issuer is the OIDC issuer URL.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Format=uri
	Issuer string `json:"issuer"`
	// Audiences are the allowed JWT audiences.
	// +kubebuilder:validation:MinItems=1
	Audiences []string `json:"audiences"`
	// AllowedClients are the allowed OAuth client IDs.
	// +optional
	AllowedClients []string `json:"allowedClients,omitempty"`
	// AllowedScopes are the allowed OAuth scopes.
	// +optional
	AllowedScopes []string `json:"allowedScopes,omitempty"`
}

// TenantConfigSpec defines the desired state of TenantConfig.
type TenantConfigSpec struct {
	// UserVerifierURL is the developer-managed endpoint for session binding.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
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
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced

// TenantConfig is the Schema for the tenantconfigs API.
type TenantConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TenantConfigSpec   `json:"spec,omitempty"`
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
