package controller_test

import (
	"testing"

	v1alpha1 "mycelium.io/mycelium/api/v1alpha1"
	"mycelium.io/mycelium/internal/controller"

	agwv1alpha1 "github.com/agentgateway/agentgateway/controller/api/v1alpha1/agentgateway"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	knservingv1 "knative.dev/serving/pkg/apis/serving/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// findCondition returns the condition with the given type, or nil if not found.
func findCondition(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}

// newClientWithIndexes creates a fake client with field indexes registered
// for MatchingFields lookups in reconciler delete handlers.
func newClientWithIndexes(t *testing.T, scheme *runtime.Scheme, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(
			&v1alpha1.MyceliumCredentialProvider{},
			&v1alpha1.MyceliumTool{},
			&v1alpha1.MyceliumAgent{},
			&v1alpha1.MyceliumEcosystem{},
		).
		WithIndex(&v1alpha1.MyceliumTool{}, controller.IndexToolCredentialBindings, func(obj client.Object) []string {
			tool := obj.(*v1alpha1.MyceliumTool)
			var refs []string
			for _, cr := range tool.Spec.Credentials {
				refs = append(refs, cr.ProviderName())
			}
			return refs
		}).
		WithIndex(&v1alpha1.MyceliumAgent{}, controller.IndexAgentToolBindings, func(obj client.Object) []string {
			agent := obj.(*v1alpha1.MyceliumAgent)
			var refs []string
			for _, t := range agent.Spec.Tools {
				refs = append(refs, t.Ref.Name)
			}
			return refs
		}).
		Build()
}

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(v1alpha1.AddToScheme(s))
	utilruntime.Must(gwv1.Install(s))
	utilruntime.Must(agwv1alpha1.AddToScheme(s))
	utilruntime.Must(knservingv1.AddToScheme(s))
	return s
}
