package webhook

import (
	"context"
	"fmt"

	v1alpha1 "github.com/mongodb/mycelium/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CredentialProviderValidator validates CredentialProvider operations.
type CredentialProviderValidator struct {
	client.Client
}

// ValidateCreate checks that the namespace is a Mycelium project (not being
// deleted) and that the referenced Secret exists.
func (v *CredentialProviderValidator) ValidateCreate(ctx context.Context, cp *v1alpha1.CredentialProvider) error {
	projectName := cp.Namespace
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

	return v.validateSecret(ctx, cp)
}

// ValidateUpdate re-checks the referenced Secret in case it changed.
func (v *CredentialProviderValidator) ValidateUpdate(ctx context.Context, cp *v1alpha1.CredentialProvider) error {
	return v.validateSecret(ctx, cp)
}

// ValidateDelete checks that no Tools reference this CredentialProvider.
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

func (v *CredentialProviderValidator) validateSecret(ctx context.Context, cp *v1alpha1.CredentialProvider) error {
	var secretName string
	if cp.IsOAuth() {
		secretName = cp.Spec.OAuth.ClientSecretRef.Name
	} else {
		secretName = cp.Spec.APIKey.APIKeySecretRef.Name
	}

	var secret corev1.Secret
	if err := v.Get(ctx, types.NamespacedName{Name: secretName, Namespace: cp.Namespace}, &secret); err != nil {
		if errors.IsNotFound(err) {
			return fmt.Errorf("Secret %s not found", secretName)
		}
		return fmt.Errorf("checking Secret %s: %w", secretName, err)
	}
	return nil
}
