package controller_test

import (
	"testing"

	v1alpha1 "github.com/mongodb/mycelium/api/v1alpha1"
	agwv1alpha1 "github.com/agentgateway/agentgateway/controller/api/v1alpha1/agentgateway"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	knservingv1 "knative.dev/serving/pkg/apis/serving/v1"
)

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
