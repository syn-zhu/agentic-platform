package wellknown

import (
	v1alpha1 "mycelium.io/mycelium/api/v1alpha1"
)

var (
	MyceliumEcosystemGVK  = v1alpha1.GroupVersion.WithKind("MyceliumEcosystem")
	IdentityProviderGVK   = v1alpha1.GroupVersion.WithKind("IdentityProvider")
	ToolGVK               = v1alpha1.GroupVersion.WithKind("Tool")
	AgentGVK              = v1alpha1.GroupVersion.WithKind("Agent")
	CredentialProviderGVK = v1alpha1.GroupVersion.WithKind("CredentialProvider")
)
