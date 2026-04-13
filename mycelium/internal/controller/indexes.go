package controller

import (
	"context"

	v1alpha1 "github.com/mongodb/mycelium/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const (
	// IndexToolCredentialProviderRefs indexes Tools by their referenced CredentialProvider names.
	IndexToolCredentialProviderRefs = "spec.credentials.providerRefs"
	// IndexAgentToolRefs indexes Agents by their referenced Tool names.
	IndexAgentToolRefs = "spec.tools.refs"
)

// SetupIndexes registers field indexes required by reconcilers and webhooks.
func SetupIndexes(ctx context.Context, mgr manager.Manager) error {
	// Tool → CredentialProvider refs (OAuth + API keys)
	if err := mgr.GetFieldIndexer().IndexField(ctx, &v1alpha1.Tool{},
		IndexToolCredentialProviderRefs,
		func(obj client.Object) []string {
			tool := obj.(*v1alpha1.Tool)
			if tool.Spec.Credentials == nil {
				return nil
			}
			var refs []string
			if tool.Spec.Credentials.OAuth != nil {
				refs = append(refs, tool.Spec.Credentials.OAuth.ProviderRef.Name)
			}
			for _, ak := range tool.Spec.Credentials.APIKeys {
				refs = append(refs, ak.ProviderRef.Name)
			}
			return refs
		},
	); err != nil {
		return err
	}

	// Agent → Tool refs
	if err := mgr.GetFieldIndexer().IndexField(ctx, &v1alpha1.Agent{},
		IndexAgentToolRefs,
		func(obj client.Object) []string {
			agent := obj.(*v1alpha1.Agent)
			var refs []string
			for _, t := range agent.Spec.Tools {
				refs = append(refs, t.Ref.Name)
			}
			return refs
		},
	); err != nil {
		return err
	}

	return nil
}
