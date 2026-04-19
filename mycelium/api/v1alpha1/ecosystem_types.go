package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	EcosystemReadyCondition          = "Ready"
	EcosystemReadyInitializingReason = "Initializing"

	EcosystemNamespaceReadyCondition            = "NamespaceReady"
	EcosystemGatewayReadyCondition              = "GatewayReady"
	EcosystemAuthenticationPolicyReadyCondition = "AuthenticationPolicyReady"
	EcosystemToolServerReadyCondition           = "ToolServerReady"
	EcosystemToolServerRouteReadyCondition      = "ToolServerRouteReady"
)

// MyceliumEcosystemSpec defines the desired state of Project.
type MyceliumEcosystemSpec struct {
	// UserVerifierEndpoint is the developer-managed endpoint for session binding.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:Format=uri
	UserVerifierEndpoint *string `json:"userVerifierEndpoint"`

	Authentication AuthenticationConfig `json:"authentication"`
}

type AuthenticationConfig struct {
	// TODO: support API key based authentication as well
	IdentityProviders []IdentityProviderConfig `json:"identityProviders"`
}

// MyceliumEcosystemStatus defines the observed state of Project.
type MyceliumEcosystemStatus struct {
	BaseMyceliumResourceStatus `json:",inline"`

	// TODO
	// Namespace            ReferencedResourceStatus `json:"namespace"`
	// Gateway              ReferencedResourceStatus `json:"gateway"`
	// ToolServer           ReferencedResourceStatus `json:"toolServer"`
	// ToolServerRoute      ReferencedResourceStatus `json:"toolServerRoute"`
	// ToolAccessPolicy     ReferencedResourceStatus `json:"toolAccessPolicy"`
	// AuthenticationPolicy ReferencedResourceStatus `json:"authenticationPolicy"`
}

func (e *MyceliumEcosystem) HasReadyCondition() bool {
	return meta.FindStatusCondition(e.Status.Conditions, EcosystemReadyCondition) != nil
}

// IdentityProviderConfig defines the desired state of an IdentityProvider.
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
	// JWKSEndpoint is the full URL of the JWKS document (e.g. https://www.googleapis.com/oauth2/v3/certs).
	// The controller creates an ExternalName Service for the hostname and derives the path for the JWT policy.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:Format=uri
	JWKSEndpoint string `json:"jwksEndpoint"`
}

// GetConditions and SetConditions implement conditions.Setter so that
// conditions.Set(&proj, cond) stamps ObservedGeneration from proj.GetGeneration()
// onto each condition.
func (e *MyceliumEcosystem) GetConditions() []metav1.Condition  { return e.Status.Conditions }
func (e *MyceliumEcosystem) SetConditions(c []metav1.Condition) { e.Status.Conditions = c }

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=proj,categories=mycelium
// +kubebuilder:printcolumn:name="Namespace",type=string,JSONPath=".status.namespace.name",description="The namespace for this project"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=='Ready')].status`,description="Whether the project is ready"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// MyceliumEcosystem is the Schema for the projects API. Each MyceliumEcosystem is cluster-scoped
// and owns a namespace where Tools and CredentialProviders live.
type MyceliumEcosystem struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +required
	Spec   MyceliumEcosystemSpec   `json:"spec"`
	Status MyceliumEcosystemStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MyceliumEcosystemList contains a list of Project.
type MyceliumEcosystemList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MyceliumEcosystem `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MyceliumEcosystem{}, &MyceliumEcosystemList{})
}
