package krtcontroller

import (
	istioconfig "istio.io/istio/pkg/config"
	"istio.io/istio/pkg/config/schema/kubetypes"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	v1alpha1 "mycelium.io/mycelium/api/v1alpha1"
)

// init registers all Mycelium CRD types with the Istio kubetypes registry so
// that krt.NewInformer[T] can resolve their GVK/GVR. Every type that will be
// used as a krt.NewInformer source must be listed here.
func init() {
	registerKRT(v1alpha1.MyceliumEcosystemGVK, &v1alpha1.MyceliumEcosystem{})
	registerKRT(v1alpha1.MyceliumAuthorizerGVK, &v1alpha1.MyceliumAuthorizer{})
	registerKRT(v1alpha1.MyceliumToolGVK, &v1alpha1.MyceliumTool{})
	registerKRT(v1alpha1.MyceliumAgentGVK, &v1alpha1.MyceliumAgent{})
	registerKRT(v1alpha1.MyceliumCredentialProviderGVK, &v1alpha1.MyceliumCredentialProvider{})
}

// krtReg is a generic kubetypes.RegisterType[T] implementation.
type krtReg[T runtime.Object] struct {
	gvk istioconfig.GroupVersionKind
	gvr schema.GroupVersionResource
	obj T
}

func (r krtReg[T]) GetGVK() istioconfig.GroupVersionKind { return r.gvk }
func (r krtReg[T]) GetGVR() schema.GroupVersionResource  { return r.gvr }
func (r krtReg[T]) Object() T                            { return r.obj }

// TODO: fixme
// registerKRT builds a krtReg from a k8s GVK and registers it with Istio's
// kubetypes registry. The plural resource name is derived by lowercasing the
// kind and appending "s". This is valid for all current Mycelium types because
// none override the default plural via +kubebuilder:resource:plural=. If a
// future type does, pass the GVR explicitly instead.
func registerKRT[T runtime.Object](gvk schema.GroupVersionKind, zero T) {
	gvr, _ := meta.UnsafeGuessKindToResource(gvk)
	kubetypes.Register(krtReg[T]{
		gvk: istioconfig.GroupVersionKind{
			Group:   gvk.Group,
			Version: gvk.Version,
			Kind:    gvk.Kind,
		},
		gvr: gvr,
		obj: zero,
	})
}
