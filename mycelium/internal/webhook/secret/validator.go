package secret

import (
	"context"
	"fmt"

	v1alpha1 "mycelium.io/mycelium/api/v1alpha1"
	"mycelium.io/mycelium/internal/controller"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// +kubebuilder:webhook:path=/validate--v1-secret,mutating=false,failurePolicy=fail,sideEffects=None,groups="",resources=secrets,verbs=delete,versions=v1,name=vsecret.mycelium.io,admissionReviewVersions=v1

// Validator blocks deletion of Secrets that are still referenced by an active CredentialProvider.
type Validator struct {
	client.Client
}

var _ admission.Validator[*corev1.Secret] = &Validator{}

func (v *Validator) ValidateCreate(_ context.Context, _ *corev1.Secret) (admission.Warnings, error) {
	return nil, nil
}

func (v *Validator) ValidateUpdate(_ context.Context, _, _ *corev1.Secret) (admission.Warnings, error) {
	return nil, nil
}

// ValidateDelete rejects the deletion if any active CredentialProvider references this Secret.
func (v *Validator) ValidateDelete(ctx context.Context, secret *corev1.Secret) (admission.Warnings, error) {
	var cps v1alpha1.CredentialProviderList
	if err := v.List(ctx, &cps,
		client.InNamespace(secret.Namespace),
		client.MatchingFields{controller.IndexCredentialProviderSecrets: secret.Name},
	); err != nil {
		return nil, fmt.Errorf("listing CredentialProviders: %w", err)
	}
	if len(cps.Items) > 0 {
		return nil, fmt.Errorf("Secret %s is referenced by CredentialProvider %s", secret.Name, cps.Items[0].Name)
	}
	return nil, nil
}
