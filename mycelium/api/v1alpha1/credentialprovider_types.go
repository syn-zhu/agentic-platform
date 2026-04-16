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
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:Format=uri
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
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:Format=uri
	Issuer string `json:"issuer"`
	// AuthorizationEndpoint is the authorization endpoint URL.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:Format=uri
	AuthorizationEndpoint string `json:"authorizationEndpoint"`
	// TokenEndpoint is the token endpoint URL.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:Format=uri
	TokenEndpoint string `json:"tokenEndpoint"`
	// ResponseTypes are the supported response types.
	// +optional
	// +kubebuilder:validation:MaxItems=8
	// +kubebuilder:validation:XValidation:rule="self.all(r, size(r) >= 1 && size(r) <= 64)",message="each response type must be 1-64 characters"
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

// OAuthCredentialProviderSpec configures an OAuth 2.0 credential provider.
type OAuthCredentialProviderSpec struct {
	// ClientID is the OAuth client ID.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	ClientID string `json:"clientId"`
	// ClientSecretRef references a key in a Kubernetes Secret containing the client secret.
	// +kubebuilder:validation:Required
	ClientSecretRef corev1.SecretKeySelector `json:"clientSecretRef"`
	// Discovery contains the OAuth2 provider discovery configuration.
	// +kubebuilder:validation:Required
	Discovery OAuthDiscovery `json:"discovery"`
}

// APIKeyCredentialProviderSpec configures an API key credential provider.
type APIKeyCredentialProviderSpec struct {
	// APIKeySecretRef references a key in a Kubernetes Secret containing the API key.
	// The key value is passed in the request body to the tool executor.
	// +kubebuilder:validation:Required
	APIKeySecretRef corev1.SecretKeySelector `json:"apiKeySecretRef"`
}

// CredentialProviderType identifies the type of credential provider.
// +kubebuilder:validation:Enum=OAuth;APIKey
type CredentialProviderType string

const (
	CredentialProviderTypeOAuth  CredentialProviderType = "OAuth"
	CredentialProviderTypeAPIKey CredentialProviderType = "APIKey"
)

// CredentialProviderSpec defines the desired state of CredentialProvider.
//
// +kubebuilder:validation:XValidation:message="oauth must be set when type is OAuth",rule="self.type == 'OAuth' ? has(self.oauth) : !has(self.oauth)"
// +kubebuilder:validation:XValidation:message="apiKey must be set when type is APIKey",rule="self.type == 'APIKey' ? has(self.apiKey) : !has(self.apiKey)"
type CredentialProviderSpec struct {
	// Type is the type of credential provider.
	// +unionDiscriminator
	// +kubebuilder:validation:Required
	Type CredentialProviderType `json:"type"`
	// OAuth configures this as an OAuth 2.0 credential provider.
	// +optional
	OAuth *OAuthCredentialProviderSpec `json:"oauth,omitempty"`
	// APIKey configures this as an API key credential provider.
	// +optional
	APIKey *APIKeyCredentialProviderSpec `json:"apiKey,omitempty"`
}

// CredentialProviderStatus defines the observed state of CredentialProvider.
type CredentialProviderStatus struct {
	BaseStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=cp,categories=mycelium
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=='Ready')].status`,description="Whether the provider is ready"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// CredentialProvider is the Schema for the credentialproviders API.
// It represents either an OAuth 2.0 provider or an API key provider.
type CredentialProvider struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +required
	Spec   CredentialProviderSpec   `json:"spec"`
	Status CredentialProviderStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// CredentialProviderList contains a list of CredentialProvider.
type CredentialProviderList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CredentialProvider `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CredentialProvider{}, &CredentialProviderList{})
}
