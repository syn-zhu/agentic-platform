package webhook

import (
	"context"
	"fmt"

	v1alpha1 "github.com/mongodb/mycelium/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ToolValidator validates Tool operations.
type ToolValidator struct {
	client.Client
}

// ValidateCreate checks that the namespace is a Mycelium project (not being
// deleted, namespace provisioned) and that all credential provider refs exist,
// are not being deleted, and are of the correct type.
func (v *ToolValidator) ValidateCreate(ctx context.Context, tool *v1alpha1.Tool) error {
	projectName := tool.Namespace

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

	return v.validateCredentialRefs(ctx, tool)
}

// ValidateUpdate re-checks credential refs in case new ones were added.
func (v *ToolValidator) ValidateUpdate(ctx context.Context, tool *v1alpha1.Tool) error {
	return v.validateCredentialRefs(ctx, tool)
}

// ValidateDelete checks that no Agents reference this Tool.
func (v *ToolValidator) ValidateDelete(ctx context.Context, tool *v1alpha1.Tool) error {
	var agentList v1alpha1.AgentList
	if err := v.List(ctx, &agentList,
		client.InNamespace(tool.Namespace),
		client.MatchingFields{"spec.tools.refs": tool.Name},
	); err != nil {
		return fmt.Errorf("listing Agents: %w", err)
	}

	if len(agentList.Items) > 0 {
		return fmt.Errorf("cannot delete Tool %s: referenced by %d Agent(s)",
			tool.Name, len(agentList.Items))
	}
	return nil
}

func (v *ToolValidator) validateCredentialRefs(ctx context.Context, tool *v1alpha1.Tool) error {
	if tool.Spec.Credentials == nil {
		return nil
	}

	if oauth := tool.Spec.Credentials.OAuth; oauth != nil {
		cp, err := v.getCredentialProvider(ctx, tool.Namespace, oauth.ProviderRef.Name)
		if err != nil {
			return err
		}
		if !cp.IsOAuth() {
			return fmt.Errorf("CredentialProvider %s is not an OAuth provider", oauth.ProviderRef.Name)
		}
	}

	for _, ak := range tool.Spec.Credentials.APIKeys {
		cp, err := v.getCredentialProvider(ctx, tool.Namespace, ak.ProviderRef.Name)
		if err != nil {
			return err
		}
		if !cp.IsAPIKey() {
			return fmt.Errorf("CredentialProvider %s is not an API key provider", ak.ProviderRef.Name)
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
