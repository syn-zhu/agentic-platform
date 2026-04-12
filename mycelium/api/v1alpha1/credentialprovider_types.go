package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// OAuthDiscovery contains discovery information for an OAuth2 provider.
// Exactly one of discoveryUrl or authorizationServerMetadata must be set.
// +kubebuilder:validation:ExactlyOneOf=discoveryUrl;authorizationServerMetadata
type OAuthDiscovery struct {
	// DiscoveryURL is the OIDC discovery endpoint.
	// +optional
	// +kubebuilder:validation:Pattern=`.+/\.well-known/openid-configuration`
	DiscoveryURL string `json:"discoveryUrl,omitempty"`
	// AuthorizationServerMetadata provides explicit OAuth2 server endpoints.
	// +optional
	AuthorizationServerMetadata *OAuthAuthorizationServerMetadata `json:"authorizationServerMetadata,omitempty"`
}

// OAuthAuthorizationServerMetadata contains the authorization server metadata
// for an OAuth2 provider.
type OAuthAuthorizationServerMetadata struct {
	// Issuer is the issuer URL for the OAuth2 authorization server.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Format=uri
	Issuer string `json:"issuer"`
	// AuthorizationEndpoint is the authorization endpoint URL.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Format=uri
	AuthorizationEndpoint string `json:"authorizationEndpoint"`
	// TokenEndpoint is the token endpoint URL.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Format=uri
	TokenEndpoint string `json:"tokenEndpoint"`
	// ResponseTypes are the supported response types.
	// +optional
	ResponseTypes []string `json:"responseTypes,omitempty"`
	// TokenEndpointAuthMethods are the authentication methods supported by the token endpoint.
	// +optional
	// +kubebuilder:validation:MaxItems=2
	TokenEndpointAuthMethods []TokenEndpointAuthMethod `json:"tokenEndpointAuthMethods,omitempty"`
}

// TokenEndpointAuthMethod specifies how clients authenticate at the token endpoint.
// +kubebuilder:validation:Enum=client_secret_post;client_secret_basic
type TokenEndpointAuthMethod string

const (
	TokenEndpointAuthMethodPost  TokenEndpointAuthMethod = "client_secret_post"
	TokenEndpointAuthMethodBasic TokenEndpointAuthMethod = "client_secret_basic"
)

// OAuthProviderSpec configures an OAuth 2.0 credential provider.
type OAuthProviderSpec struct {
	// ClientID is the OAuth client ID.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ClientID string `json:"clientId"`
	// ClientSecretRef references a key in a Kubernetes Secret containing the client secret.
	// +kubebuilder:validation:Required
	ClientSecretRef corev1.SecretKeySelector `json:"clientSecretRef"`
	// Discovery contains the OAuth2 provider discovery configuration.
	// +kubebuilder:validation:Required
	Discovery OAuthDiscovery `json:"discovery"`
}

// APIKeyProviderSpec configures an API key credential provider.
type APIKeyProviderSpec struct {
	// APIKeySecretRef references a key in a Kubernetes Secret containing the API key.
	// The key value is passed in the request body to the tool executor.
	// +kubebuilder:validation:Required
	APIKeySecretRef corev1.SecretKeySelector `json:"apiKeySecretRef"`
}

// CredentialProviderSpec defines the desired state of CredentialProvider.
// Exactly one of oauth or apiKey must be set.
// +kubebuilder:validation:ExactlyOneOf=oauth;apiKey
type CredentialProviderSpec struct {
	// OAuth configures this as an OAuth 2.0 credential provider.
	// +optional
	OAuth *OAuthProviderSpec `json:"oauth,omitempty"`
	// APIKey configures this as an API key credential provider.
	// +optional
	APIKey *APIKeyProviderSpec `json:"apiKey,omitempty"`
}

// CredentialProviderStatus defines the observed state of CredentialProvider.
type CredentialProviderStatus struct {
	// CallbackURL is the generated OAuth callback URL (only set for OAuth providers).
	// +optional
	CallbackURL string `json:"callbackUrl,omitempty"`
	// Conditions represent the latest observations.
	// Known condition types: "Ready"
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced

// CredentialProvider is the Schema for the credentialproviders API.
// It represents either an OAuth 2.0 provider or an API key provider.
type CredentialProvider struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CredentialProviderSpec   `json:"spec,omitempty"`
	Status CredentialProviderStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// CredentialProviderList contains a list of CredentialProvider.
type CredentialProviderList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CredentialProvider `json:"items"`
}

// IsOAuth returns true if this is an OAuth credential provider.
func (cp *CredentialProvider) IsOAuth() bool {
	return cp.Spec.OAuth != nil
}

// IsAPIKey returns true if this is an API key credential provider.
func (cp *CredentialProvider) IsAPIKey() bool {
	return cp.Spec.APIKey != nil
}

func init() {
	SchemeBuilder.Register(&CredentialProvider{}, &CredentialProviderList{})
}
