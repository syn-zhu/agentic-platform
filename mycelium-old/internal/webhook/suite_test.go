package webhook_test

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	v1alpha1 "mycelium.io/mycelium/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	utilruntime.Must(v1alpha1.AddToScheme(s))
	return s
}

// newClientWithIndexes creates a fake client with the credential provider refs index registered.
func newClientWithIndexes(t *testing.T, scheme *runtime.Scheme, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithIndex(&v1alpha1.MyceliumTool{}, "spec.credentials.providerRefs", func(obj client.Object) []string {
			tool := obj.(*v1alpha1.MyceliumTool)
			var refs []string
			for _, cr := range tool.Spec.Credentials {
				refs = append(refs, cr.ProviderName())
			}
			return refs
		}).
		Build()
}
