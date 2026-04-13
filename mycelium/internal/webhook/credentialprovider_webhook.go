package webhook

import (
	"context"
	"fmt"

	v1alpha1 "github.com/mongodb/mycelium/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CredentialProviderValidator validates CredentialProvider operations.
type CredentialProviderValidator struct {
	client.Client
}

// ValidateCreate checks that the namespace is a Mycelium project and the
// Project is not being deleted.
func (v *CredentialProviderValidator) ValidateCreate(ctx context.Context, cp *v1alpha1.CredentialProvider) error {
	// Check Project exists (project name == namespace name)
	projectName := cp.Namespace
	var proj v1alpha1.Project
	if err := v.Get(ctx, types.NamespacedName{Name: projectName}, &proj); err != nil {
		if errors.IsNotFound(err) {
			return fmt.Errorf("Project %s not found", projectName)
		}
		return fmt.Errorf("checking Project: %w", err)
	}

	// Check Project is not being deleted
	if !proj.DeletionTimestamp.IsZero() {
		return fmt.Errorf("Project %s is being deleted", projectName)
	}

	// Check Project namespace is provisioned
	if proj.Status.NamespaceRef == nil {
		return fmt.Errorf("Project %s namespace not yet provisioned", projectName)
	}

	return nil
}

// ValidateDelete checks that no Tools reference this CredentialProvider.
// Uses the spec.credentials.providerRefs field index for efficient lookup.
func (v *CredentialProviderValidator) ValidateDelete(ctx context.Context, cp *v1alpha1.CredentialProvider) error {
	var toolList v1alpha1.ToolList
	if err := v.List(ctx, &toolList,
		client.InNamespace(cp.Namespace),
		client.MatchingFields{"spec.credentials.providerRefs": cp.Name},
	); err != nil {
		return fmt.Errorf("listing Tools: %w", err)
	}

	if len(toolList.Items) > 0 {
		return fmt.Errorf("cannot delete CredentialProvider %s: referenced by %d Tool(s)",
			cp.Name, len(toolList.Items))
	}
	return nil
}
