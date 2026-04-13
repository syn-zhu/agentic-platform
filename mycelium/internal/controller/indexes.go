package controller

import (
	"context"

	v1alpha1 "github.com/mongodb/mycelium/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const (
	// IndexToolCredentialBindings indexes Tools by their referenced CredentialProvider names.
	IndexToolCredentialBindings = "spec.credentials.providerRefs"
	// IndexAgentToolRefs indexes Agents by their referenced Tool names.
	IndexAgentToolRefs = "spec.tools.refs"
)

// SetupIndexes registers field indexes required by reconcilers and webhooks.
func SetupIndexes(ctx context.Context, mgr manager.Manager) error {
	// Tool → CredentialProvider refs
	if err := mgr.GetFieldIndexer().IndexField(ctx, &v1alpha1.Tool{},
		IndexToolCredentialBindings,
		func(obj client.Object) []string {
			tool := obj.(*v1alpha1.Tool)
			var refs []string
			for _, cr := range tool.Spec.Credentials {
				refs = append(refs, cr.ProviderName())
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
