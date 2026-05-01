package credentialprovider

import (
	"context"

	v1alpha1 "mycelium.io/mycelium/api/v1alpha1"
	"mycelium.io/mycelium/internal/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// Defaulter sets defaults on CredentialProvider resources (e.g., adds finalizer).
type Defaulter struct{}

var _ admission.Defaulter[*v1alpha1.MyceliumCredentialProvider] = &Defaulter{}

func (d *Defaulter) Default(_ context.Context, cp *v1alpha1.MyceliumCredentialProvider) error {
	controllerutil.AddFinalizer(cp, controller.CredentialProviderFinalizer)
	return nil
}
