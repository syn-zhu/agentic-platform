package wellknown

import (
	agwv1alpha1 "github.com/agentgateway/agentgateway/controller/api/v1alpha1/agentgateway"
	v1alpha1 "mycelium.io/mycelium/api/v1alpha1"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

var (
	ProjectGVK            = v1alpha1.GroupVersion.WithKind("Project")
	ToolGVK               = v1alpha1.GroupVersion.WithKind("Tool")
	AgentGVK              = v1alpha1.GroupVersion.WithKind("Agent")
	CredentialProviderGVK = v1alpha1.GroupVersion.WithKind("CredentialProvider")

	AgentgatewayBackendGVK = agwv1alpha1.SchemeGroupVersion.WithKind("AgentgatewayBackend")
	AgentgatewayPolicyGVK  = agwv1alpha1.SchemeGroupVersion.WithKind("AgentgatewayPolicy")
	HTTPRouteGVK           = gwv1.SchemeGroupVersion.WithKind("HTTPRoute")
)
