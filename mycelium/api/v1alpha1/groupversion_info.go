package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	GroupVersion  = schema.GroupVersion{Group: "mycelium.io", Version: "v1alpha1"}
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}
	AddToScheme   = SchemeBuilder.AddToScheme
)

// GVK constants for all Mycelium API types. Derived from GroupVersion so they
// stay in sync automatically. Add a new entry here when adding a new CRD type.
var (
	MyceliumEcosystemGVK          = GroupVersion.WithKind("MyceliumEcosystem")
	MyceliumAuthorizerGVK         = GroupVersion.WithKind("MyceliumAuthorizer")
	MyceliumToolGVK               = GroupVersion.WithKind("MyceliumTool")
	MyceliumAgentGVK              = GroupVersion.WithKind("MyceliumAgent")
	MyceliumCredentialProviderGVK = GroupVersion.WithKind("MyceliumCredentialProvider")
)

