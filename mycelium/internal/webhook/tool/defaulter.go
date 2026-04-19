package tool

import (
	"context"

	v1alpha1 "mycelium.io/mycelium/api/v1alpha1"
	"mycelium.io/mycelium/internal/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// Defaulter sets defaults on Tool resources (e.g., adds finalizer).
type Defaulter struct{}

var _ admission.Defaulter[*v1alpha1.MyceliumTool] = &Defaulter{}

func (d *Defaulter) Default(_ context.Context, tool *v1alpha1.MyceliumTool) error {
	controllerutil.AddFinalizer(tool, controller.ToolFinalizer)
	return nil
}
