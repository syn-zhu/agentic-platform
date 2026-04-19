package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// OAuthDiscoveryConfig contains discovery information for an OAuth2 provider.
// Exactly one of discoveryUrl or authorizationServerMetadata must be set.
// +kubebuilder:validation:ExactlyOneOf=discoveryUrl;authorizationServerMetadata
type OAuthDiscoveryConfig struct {
	// DiscoveryURL is the OIDC discovery endpoint.
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:Format=uri
	// +kubebuilder:validation:Pattern=`.+/\.well-known/openid-configuration`
	DiscoveryURL *string `json:"discoveryUrl,omitempty"`
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

// OAuthCredentialProviderConfig configures an OAuth 2.0 credential provider.
type OAuthCredentialProviderConfig struct {
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
	Discovery OAuthDiscoveryConfig `json:"discovery"`
}

// APIKeyCredentialProviderConfig configures an API key credential provider.
type APIKeyCredentialProviderConfig struct {
	// SecretRef references a key in a Kubernetes Secret containing the API key.
	// The key value is passed in the request body to the tool executor.
	// +kubebuilder:validation:Required
	SecretRef corev1.SecretKeySelector `json:"secretRef"`
}

// CredentialProviderType identifies the type of credential provider.
// +kubebuilder:validation:Enum=OAuth;APIKey
type CredentialProviderType string

const (
	CredentialProviderTypeOAuth  CredentialProviderType = "OAuth"
	CredentialProviderTypeAPIKey CredentialProviderType = "APIKey"
)

// MyceliumCredentialProviderSpec defines the desired state of CredentialProvider.
//
// +kubebuilder:validation:XValidation:message="oauth must be set when type is OAuth",rule="self.type == 'OAuth' ? has(self.oauth) : !has(self.oauth)"
// +kubebuilder:validation:XValidation:message="apiKey must be set when type is APIKey",rule="self.type == 'APIKey' ? has(self.apiKey) : !has(self.apiKey)"
type MyceliumCredentialProviderSpec struct {
	// Type is the type of credential provider.
	// +unionDiscriminator
	// +kubebuilder:validation:Required
	Type CredentialProviderType `json:"type"`
	// OAuth configures this as an OAuth 2.0 credential provider.
	// +optional
	OAuth *OAuthCredentialProviderConfig `json:"oauth,omitempty"`
	// APIKey configures this as an API key credential provider.
	// +optional
	APIKey *APIKeyCredentialProviderConfig `json:"apiKey,omitempty"`
}

// MyceliumCredentialProviderStatus defines the observed state of CredentialProvider.
type MyceliumCredentialProviderStatus struct {
	BaseMyceliumResourceStatus `json:",inline"`
}

// GetConditions and SetConditions implement conditions.Setter so that
// conditions.Set(&cp, cond) stamps ObservedGeneration from cp.GetGeneration()
// onto each condition.
func (cp *MyceliumCredentialProvider) GetConditions() []metav1.Condition  { return cp.Status.Conditions }
func (cp *MyceliumCredentialProvider) SetConditions(c []metav1.Condition) { cp.Status.Conditions = c }

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=cp,categories=mycelium
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=='Ready')].status`,description="Whether the provider is ready"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// MyceliumCredentialProvider is the Schema for the credentialproviders API.
// It represents either an OAuth 2.0 provider or an API key provider.
type MyceliumCredentialProvider struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +required
	Spec   MyceliumCredentialProviderSpec   `json:"spec"`
	Status MyceliumCredentialProviderStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MyceliumCredentialProviderList contains a list of CredentialProvider.
type MyceliumCredentialProviderList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MyceliumCredentialProvider `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MyceliumCredentialProvider{}, &MyceliumCredentialProviderList{})
}
