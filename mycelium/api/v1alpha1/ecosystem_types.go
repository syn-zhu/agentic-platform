package v1alpha1

import (
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
	UserVerifierEndpoint string `json:"userVerifierEndpoint"`
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
