package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AuthorizerType is the type of authorizer.
// +kubebuilder:validation:Enum=JWT
type AuthorizerType string

const (
	AuthorizerTypeJWT AuthorizerType = "JWT"

	AuthorizerReadyCondition = "Ready"
)

// MyceliumAuthorizerSpec defines the desired state of an Authorizer.
//
// +kubebuilder:validation:XValidation:message="jwt must be set when type is JWT",rule="self.type == 'JWT' ? has(self.jwt) : !has(self.jwt)"
type MyceliumAuthorizerSpec struct {
	// Type is the type of authorizer.
	// +unionDiscriminator
	// +kubebuilder:validation:Required
	Type AuthorizerType `json:"type"`

	// JWT configures JWT-based authorization.
	// +optional
	JWT *JWTAuthorizerConfig `json:"jwt,omitempty"`
}

// JWTAuthorizerConfig configures JWT-based authorization via an OIDC-compatible identity provider.
type JWTAuthorizerConfig struct {
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

	// JWKSEndpoint is the full URL of the JWKS document
	// (e.g. https://www.googleapis.com/oauth2/v3/certs).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:Format=uri
	JWKSEndpoint string `json:"jwksEndpoint"`
}

// MyceliumAuthorizerStatus defines the observed state of an Authorizer.
type MyceliumAuthorizerStatus struct {
	BaseMyceliumResourceStatus `json:",inline"`
}

// GetConditions and SetConditions implement conditions.Setter so that
// conditions.Set(&authz, cond) stamps ObservedGeneration from authz.GetGeneration().
func (a *MyceliumAuthorizer) GetConditions() []metav1.Condition  { return a.Status.Conditions }
func (a *MyceliumAuthorizer) SetConditions(c []metav1.Condition) { a.Status.Conditions = c }

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=authz,categories=mycelium
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=".spec.type",description="The authorizer type"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=='Ready')].status`,description="Whether the authorizer is ready"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Authorizer is the Schema for the authorizers API. Each Authorizer configures
// a method by which incoming requests may be authenticated and authorized.
type MyceliumAuthorizer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +required
	Spec   MyceliumAuthorizerSpec   `json:"spec"`
	Status MyceliumAuthorizerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AuthorizerList contains a list of Authorizer.
type MyceliumAuthorizerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MyceliumAuthorizer `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MyceliumAuthorizer{}, &MyceliumAuthorizerList{})
}
