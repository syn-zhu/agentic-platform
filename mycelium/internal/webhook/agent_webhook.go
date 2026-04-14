package webhook

import (
	"context"
	"fmt"

	v1alpha1 "github.com/mongodb/mycelium/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// AgentValidator validates Agent operations.
type AgentValidator struct {
	client.Client
}

var _ admission.Validator[*v1alpha1.Agent] = &AgentValidator{}

func (v *AgentValidator) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &v1alpha1.Agent{}).
		WithValidator(v).
		Complete()
}

// ValidateCreate checks that the namespace is a Mycelium project (not being
// deleted) and that all tool refs exist and are not being deleted.
func (v *AgentValidator) ValidateCreate(ctx context.Context, agent *v1alpha1.Agent) (admission.Warnings, error) {
	projectName := agent.Namespace

	var proj v1alpha1.Project
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
func (v *AgentValidator) ValidateUpdate(ctx context.Context, _, newObj *v1alpha1.Agent) (admission.Warnings, error) {
	return nil, v.validateToolRefs(ctx, newObj)
}

func (v *AgentValidator) ValidateDelete(_ context.Context, _ *v1alpha1.Agent) (admission.Warnings, error) {
	return nil, nil
}

func (v *AgentValidator) validateToolRefs(ctx context.Context, agent *v1alpha1.Agent) error {
	for _, toolRef := range agent.Spec.Tools {
		var tool v1alpha1.Tool
		if err := v.Get(ctx, types.NamespacedName{
			Name:      toolRef.Ref.Name,
			Namespace: agent.Namespace,
		}, &tool); err != nil {
			if errors.IsNotFound(err) {
				return fmt.Errorf("Tool %s not found", toolRef.Ref.Name)
			}
			return fmt.Errorf("checking Tool %s: %w", toolRef.Ref.Name, err)
		}
		if !tool.DeletionTimestamp.IsZero() {
			return fmt.Errorf("Tool %s is being deleted", toolRef.Ref.Name)
		}
	}
	return nil
}
