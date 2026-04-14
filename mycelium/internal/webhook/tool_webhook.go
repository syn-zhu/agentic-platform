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

// ToolValidator validates Tool operations.
type ToolValidator struct {
	client.Client
}

var _ admission.Validator[*v1alpha1.Tool] = &ToolValidator{}

func (v *ToolValidator) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &v1alpha1.Tool{}).
		WithValidator(v).
		Complete()
}

// ValidateCreate checks that the namespace is a Mycelium project (not being
// deleted) and that all credential provider refs exist, are not being deleted,
// and are of the correct type.
func (v *ToolValidator) ValidateCreate(ctx context.Context, tool *v1alpha1.Tool) (admission.Warnings, error) {
	projectName := tool.Namespace

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

	return nil, v.validateCredentialRefs(ctx, tool)
}

// ValidateUpdate re-checks credential refs in case new ones were added.
func (v *ToolValidator) ValidateUpdate(ctx context.Context, _, newObj *v1alpha1.Tool) (admission.Warnings, error) {
	return nil, v.validateCredentialRefs(ctx, newObj)
}

// ValidateDelete checks that no Agents reference this Tool.
func (v *ToolValidator) ValidateDelete(ctx context.Context, tool *v1alpha1.Tool) (admission.Warnings, error) {
	var agentList v1alpha1.AgentList
	if err := v.List(ctx, &agentList,
		client.InNamespace(tool.Namespace),
		client.MatchingFields{"spec.tools.refs": tool.Name},
	); err != nil {
		return nil, fmt.Errorf("listing Agents: %w", err)
	}

	if len(agentList.Items) > 0 {
		return nil, fmt.Errorf("cannot delete Tool %s: referenced by %d Agent(s)",
			tool.Name, len(agentList.Items))
	}
	return nil, nil
}

func (v *ToolValidator) validateCredentialRefs(ctx context.Context, tool *v1alpha1.Tool) error {
	for _, cr := range tool.Spec.Credentials {
		name := cr.ProviderName()
		cp, err := v.getCredentialProvider(ctx, tool.Namespace, name)
		if err != nil {
			return err
		}
		if cr.IsOAuth() && !cp.IsOAuth() {
			return fmt.Errorf("CredentialProvider %s is not an OAuth provider", name)
		}
		if cr.IsAPIKey() && !cp.IsAPIKey() {
			return fmt.Errorf("CredentialProvider %s is not an API key provider", name)
		}
	}
	return nil
}

func (v *ToolValidator) getCredentialProvider(ctx context.Context, namespace, name string) (*v1alpha1.CredentialProvider, error) {
	var cp v1alpha1.CredentialProvider
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
