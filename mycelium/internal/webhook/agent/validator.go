package agent

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	v1alpha1 "mycelium.io/mycelium/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// Validator validates Agent operations.
type Validator struct {
	client.Client
}

var _ admission.Validator[*v1alpha1.MyceliumAgent] = &Validator{}

// ValidateCreate checks that the namespace is a Mycelium project (not being
// deleted) and that all tool refs exist and are not being deleted.
func (v *Validator) ValidateCreate(ctx context.Context, agent *v1alpha1.MyceliumAgent) (admission.Warnings, error) {
	projectName := agent.Namespace

	var proj v1alpha1.MyceliumEcosystem
	if err := v.Get(ctx, types.NamespacedName{Name: projectName}, &proj); err != nil {
		if errors.IsNotFound(err) {
			return nil, fmt.Errorf("Project %s not found", projectName)
		}
		return nil, fmt.Errorf("checking Project: %w", err)
	}

	if !proj.DeletionTimestamp.IsZero() {
		return nil, fmt.Errorf("Project %s is being deleted", projectName)
	}

	return nil, v.validateToolRefs(ctx, agent)
}

// ValidateUpdate re-checks tool refs in case new ones were added.
func (v *Validator) ValidateUpdate(ctx context.Context, _, newObj *v1alpha1.MyceliumAgent) (admission.Warnings, error) {
	return nil, v.validateToolRefs(ctx, newObj)
}

func (v *Validator) ValidateDelete(_ context.Context, _ *v1alpha1.MyceliumAgent) (admission.Warnings, error) {
	return nil, nil
}

func (v *Validator) validateToolRefs(ctx context.Context, agent *v1alpha1.MyceliumAgent) error {
	for _, tb := range agent.Spec.ToolBindings {
		var tool v1alpha1.MyceliumTool
		if err := v.Get(ctx, types.NamespacedName{
			Name:      tb.Tool.Name,
			Namespace: agent.Namespace,
		}, &tool); err != nil {
			if errors.IsNotFound(err) {
				return fmt.Errorf("Tool %s not found", tb.Tool.Name)
			}
			return fmt.Errorf("checking Tool %s: %w", tb.Tool.Name, err)
		}
		if !tool.DeletionTimestamp.IsZero() {
			return fmt.Errorf("Tool %s is being deleted", tb.Tool.Name)
		}
	}
	return nil
}
