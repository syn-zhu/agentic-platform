package webhook

import (
	"context"
	"fmt"

	v1alpha1 "github.com/mongodb/mycelium/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// AgentValidator validates Agent operations.
type AgentValidator struct {
	client.Client
}

// ValidateCreate checks that the namespace is a Mycelium project (not being
// deleted, namespace provisioned) and that all tool refs exist and are not
// being deleted.
func (v *AgentValidator) ValidateCreate(ctx context.Context, agent *v1alpha1.Agent) error {
	projectName := agent.Namespace

	var proj v1alpha1.Project
	if err := v.Get(ctx, types.NamespacedName{Name: projectName}, &proj); err != nil {
		if errors.IsNotFound(err) {
			return fmt.Errorf("Project %s not found", projectName)
		}
		return fmt.Errorf("checking Project: %w", err)
	}

	if !proj.DeletionTimestamp.IsZero() {
		return fmt.Errorf("Project %s is being deleted", projectName)
	}

	if proj.Status.NamespaceRef == nil {
		return fmt.Errorf("Project %s namespace not yet provisioned", projectName)
	}

	return v.validateToolRefs(ctx, agent)
}

// ValidateUpdate re-checks tool refs in case new ones were added.
func (v *AgentValidator) ValidateUpdate(ctx context.Context, agent *v1alpha1.Agent) error {
	return v.validateToolRefs(ctx, agent)
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
