package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SecretKeyRef is a reference to a key in a Kubernetes Secret.
type SecretKeyRef struct {
	// Name is the name of the Secret.
	Name string `json:"name"`
	// Key is the key within the Secret.
	Key string `json:"key"`
}

// OAuthResourceSpec defines the desired state of OAuthResource.
type OAuthResourceSpec struct {
	// AuthorizationEndpoint is the OAuth authorization URL.
	AuthorizationEndpoint string `json:"authorizationEndpoint"`
	// TokenEndpoint is the OAuth token exchange URL.
	TokenEndpoint string `json:"tokenEndpoint"`
	// ClientID is the OAuth client ID.
	ClientID string `json:"clientId"`
	// ClientSecretRef references the Kubernetes Secret containing the client secret.
	ClientSecretRef SecretKeyRef `json:"clientSecretRef"`
	// DiscoveryURL is the optional OIDC discovery endpoint. If set, endpoints
	// are auto-discovered and AuthorizationEndpoint/TokenEndpoint are ignored.
	// +optional
	DiscoveryURL string `json:"discoveryUrl,omitempty"`
}

// OAuthResourceStatus defines the observed state of OAuthResource.
type OAuthResourceStatus struct {
	// CallbackURL is the generated OAuth callback URL for this resource.
	// +optional
	CallbackURL string `json:"callbackUrl,omitempty"`
	// Conditions represent the latest observations.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced

// OAuthResource is the Schema for the oauthresources API.
type OAuthResource struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   OAuthResourceSpec   `json:"spec,omitempty"`
	Status OAuthResourceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// OAuthResourceList contains a list of OAuthResource.
type OAuthResourceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []OAuthResource `json:"items"`
}

func init() {
	SchemeBuilder.Register(&OAuthResource{}, &OAuthResourceList{})
}
