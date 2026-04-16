package project

import (
	"context"

	v1alpha1 "mycelium.io/mycelium/api/v1alpha1"
	"mycelium.io/mycelium/internal/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// Defaulter sets defaults on Project resources (e.g., adds finalizer).
type Defaulter struct{}

var _ admission.Defaulter[*v1alpha1.Project] = &Defaulter{}

func (d *Defaulter) Default(_ context.Context, proj *v1alpha1.Project) error {
	controllerutil.AddFinalizer(proj, controller.ProjectFinalizer)
	return nil
}
