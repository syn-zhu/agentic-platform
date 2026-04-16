package controller

import (
	"context"

	v1alpha1 "mycelium.io/mycelium/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const (
	// IndexToolOAuthCredentialBindings indexes active Tools by their referenced OAuth CredentialProvider names.
	IndexToolOAuthCredentialBindings = "spec.credentialBindings.oauth.credentialProvider"
	// IndexToolAPIKeyCredentialBindings indexes active Tools by their referenced APIKey CredentialProvider names.
	IndexToolAPIKeyCredentialBindings = "spec.credentialBindings.apiKey.credentialProvider"
	// IndexAgentToolBindings indexes Agents by their referenced Tool names.
	IndexAgentToolBindings = "spec.toolBindings.tool"
	// IndexCredentialProviderSecrets indexes active CredentialProviders by their referenced Secret names.
	IndexCredentialProviderSecrets = "spec.secretRef"
)

// ToolCredentialBindingIndex returns the field index name for the given CredentialProvider type.
func ToolCredentialBindingIndex(t v1alpha1.CredentialProviderType) string {
	switch t {
	case v1alpha1.CredentialProviderTypeOAuth:
		return IndexToolOAuthCredentialBindings
	case v1alpha1.CredentialProviderTypeAPIKey:
		return IndexToolAPIKeyCredentialBindings
	default:
		return ""
	}
}

// SetupIndexes registers field indexes required by reconcilers and webhooks.
func SetupIndexes(ctx context.Context, mgr manager.Manager) error {
	// Tool → OAuth CredentialProvider refs (active Tools only)
	if err := mgr.GetFieldIndexer().IndexField(ctx, &v1alpha1.Tool{},
		IndexToolOAuthCredentialBindings,
		func(obj client.Object) []string {
			tool := obj.(*v1alpha1.Tool)
			if !tool.DeletionTimestamp.IsZero() {
				return nil
			}
			var refs []string
			for _, cr := range tool.Spec.CredentialBindings {
				if cr.Type == v1alpha1.CredentialProviderTypeOAuth {
					refs = append(refs, cr.OAuth.CredentialProviderRef.Name)
				}
			}
			return refs
		},
	); err != nil {
		return err
	}

	// Tool → APIKey CredentialProvider refs (active Tools only)
	if err := mgr.GetFieldIndexer().IndexField(ctx, &v1alpha1.Tool{},
		IndexToolAPIKeyCredentialBindings,
		func(obj client.Object) []string {
			tool := obj.(*v1alpha1.Tool)
			if !tool.DeletionTimestamp.IsZero() {
				return nil
			}
			var refs []string
			for _, cr := range tool.Spec.CredentialBindings {
				if cr.Type == v1alpha1.CredentialProviderTypeAPIKey {
					refs = append(refs, cr.APIKey.CredentialProviderRef.Name)
				}
			}
			return refs
		},
	); err != nil {
		return err
	}

	// Agent → Tool refs
	if err := mgr.GetFieldIndexer().IndexField(ctx, &v1alpha1.Agent{},
		IndexAgentToolBindings,
		func(obj client.Object) []string {
			agent := obj.(*v1alpha1.Agent)
			if !agent.DeletionTimestamp.IsZero() {
				return nil
			}
			var refs []string
			for _, tb := range agent.Spec.ToolBindings {
				refs = append(refs, tb.Tool.Name)
			}
			return refs
		},
	); err != nil {
		return err
	}

	// CredentialProvider → Secret refs (active CredentialProviders only)
	if err := mgr.GetFieldIndexer().IndexField(ctx, &v1alpha1.CredentialProvider{},
		IndexCredentialProviderSecrets,
		func(obj client.Object) []string {
			cp := obj.(*v1alpha1.CredentialProvider)
			if !cp.DeletionTimestamp.IsZero() {
				return nil
			}
			switch cp.Spec.Type {
			case v1alpha1.CredentialProviderTypeOAuth:
				return []string{cp.Spec.OAuth.ClientSecretRef.Name}
			case v1alpha1.CredentialProviderTypeAPIKey:
				return []string{cp.Spec.APIKey.APIKeySecretRef.Name}
			}
			return nil
		},
	); err != nil {
		return err
	}

	return nil
}
