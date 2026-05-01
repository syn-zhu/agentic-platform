package agent

import (
	"context"

	v1alpha1 "mycelium.io/mycelium/api/v1alpha1"
	"mycelium.io/mycelium/internal/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// Defaulter sets defaults on Agent resources (e.g., adds finalizer).
type Defaulter struct{}

var _ admission.Defaulter[*v1alpha1.MyceliumAgent] = &Defaulter{}

func (d *Defaulter) Default(_ context.Context, agent *v1alpha1.MyceliumAgent) error {
	controllerutil.AddFinalizer(agent, controller.AgentFinalizer)
	return nil
}
