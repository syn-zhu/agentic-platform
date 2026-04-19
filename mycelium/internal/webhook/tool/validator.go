package tool

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	v1alpha1 "mycelium.io/mycelium/api/v1alpha1"
	"mycelium.io/mycelium/internal/indexes"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// Validator validates Tool operations.
type Validator struct {
	client.Client
}

var _ admission.Validator[*v1alpha1.MyceliumTool] = &Validator{}

// ValidateCreate checks that the namespace is a Mycelium project (not being
// deleted) and that all credential provider refs exist, are not being deleted,
// and are of the correct type.
func (v *Validator) ValidateCreate(ctx context.Context, tool *v1alpha1.MyceliumTool) (admission.Warnings, error) {
	projectName := tool.Namespace

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

	return nil, v.validateCredentialRefs(ctx, tool)
}

// ValidateUpdate re-checks credential refs in case new ones were added.
func (v *Validator) ValidateUpdate(ctx context.Context, _, newObj *v1alpha1.MyceliumTool) (admission.Warnings, error) {
	return nil, v.validateCredentialRefs(ctx, newObj)
}

// ValidateDelete checks that no Agents reference this Tool.
func (v *Validator) ValidateDelete(ctx context.Context, tool *v1alpha1.MyceliumTool) (admission.Warnings, error) {
	var agentList v1alpha1.MyceliumAgentList
	if err := v.List(ctx, &agentList,
		client.InNamespace(tool.Namespace),
		client.MatchingFields{indexes.IndexAgentToolBindings: tool.Name},
	); err != nil {
		return nil, fmt.Errorf("listing Agents: %w", err)
	}

	if len(agentList.Items) > 0 {
		return nil, fmt.Errorf("cannot delete Tool %s: referenced by %d Agent(s)",
			tool.Name, len(agentList.Items))
	}
	return nil, nil
}

func (v *Validator) validateCredentialRefs(ctx context.Context, tool *v1alpha1.MyceliumTool) error {
	for _, cr := range tool.Spec.CredentialProviderBindings {
		cp, err := v.getCredentialProvider(ctx, tool.Namespace, cr.CredentialProviderName())
		if err != nil {
			return err
		}
		if cr.Type != cp.Spec.Type {
			return fmt.Errorf("CredentialProvider %s has type %s but binding expects %s", cr.CredentialProviderName(), cp.Spec.Type, cr.Type)
		}
	}
	return nil
}

func (v *Validator) getCredentialProvider(ctx context.Context, namespace, name string) (*v1alpha1.MyceliumCredentialProvider, error) {
	var cp v1alpha1.MyceliumCredentialProvider
	if err := v.Get(ctx, types.NamespacedName{
		Name:      name,
		Namespace: namespace,
	}, &cp); err != nil {
		if errors.IsNotFound(err) {
			return nil, fmt.Errorf("CredentialProvider %s not found", name)
		}
		return nil, fmt.Errorf("checking CredentialProvider %s: %w", name, err)
	}
	if !cp.DeletionTimestamp.IsZero() {
		return nil, fmt.Errorf("CredentialProvider %s is being deleted", name)
	}
	return &cp, nil
}
