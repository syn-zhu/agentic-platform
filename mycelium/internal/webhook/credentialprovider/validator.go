package credentialprovider

import (
	"context"
	"fmt"

	v1alpha1 "mycelium.io/mycelium/api/v1alpha1"
	"mycelium.io/mycelium/internal/controller"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// Validator validates CredentialProvider operations.
type Validator struct {
	client.Client
}

var _ admission.Validator[*v1alpha1.CredentialProvider] = &Validator{}

// ValidateCreate checks that the namespace is a Mycelium project (not being
// deleted) and that the referenced Secret exists.
func (v *Validator) ValidateCreate(ctx context.Context, cp *v1alpha1.CredentialProvider) (admission.Warnings, error) {
	projectName := cp.Namespace
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

	return nil, v.validateSecret(ctx, cp)
}

// ValidateUpdate re-checks the referenced Secret in case it changed.
func (v *Validator) ValidateUpdate(ctx context.Context, _, newObj *v1alpha1.CredentialProvider) (admission.Warnings, error) {
	return nil, v.validateSecret(ctx, newObj)
}

// ValidateDelete checks that no Tools reference this CredentialProvider.
func (v *Validator) ValidateDelete(ctx context.Context, cp *v1alpha1.CredentialProvider) (admission.Warnings, error) {
	var toolList v1alpha1.ToolList
	if err := v.List(ctx, &toolList,
		client.InNamespace(cp.Namespace),
		client.MatchingFields{controller.ToolCredentialBindingIndex(cp.Spec.Type): cp.Name},
	); err != nil {
		return nil, fmt.Errorf("listing Tools: %w", err)
	}

	if len(toolList.Items) > 0 {
		return nil, fmt.Errorf("cannot delete CredentialProvider %s: referenced by %d Tool(s)",
			cp.Name, len(toolList.Items))
	}
	return nil, nil
}

func (v *Validator) validateSecret(ctx context.Context, cp *v1alpha1.CredentialProvider) error {
	var secretName string
	switch cp.Spec.Type {
	case v1alpha1.CredentialProviderTypeOAuth:
		secretName = cp.Spec.OAuth.ClientSecretRef.Name
	case v1alpha1.CredentialProviderTypeAPIKey:
		secretName = cp.Spec.APIKey.APIKeySecretRef.Name
	}

	var secret corev1.Secret
	if err := v.Get(ctx, types.NamespacedName{Name: secretName, Namespace: cp.Namespace}, &secret); err != nil {
		if errors.IsNotFound(err) {
			return fmt.Errorf("Secret %s not found", secretName)
		}
		return fmt.Errorf("checking Secret %s: %w", secretName, err)
	}

	if !secret.DeletionTimestamp.IsZero() {
		return fmt.Errorf("Secret %s is being deleted", secret.Name)
	}
	return nil
}
