package webhook

import (
	"context"
	"fmt"

	v1alpha1 "github.com/mongodb/mycelium/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// ProjectValidator validates Project operations.
type ProjectValidator struct {
	client.Client
}

var _ admission.Validator[*v1alpha1.Project] = &ProjectValidator{}

func (v *ProjectValidator) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &v1alpha1.Project{}).
		WithValidator(v).
		Complete()
}

func (v *ProjectValidator) ValidateCreate(_ context.Context, _ *v1alpha1.Project) (admission.Warnings, error) {
	return nil, nil
}

func (v *ProjectValidator) ValidateUpdate(_ context.Context, _, _ *v1alpha1.Project) (admission.Warnings, error) {
	return nil, nil
}

// ValidateDelete checks that no Tools, CredentialProviders, or Agents exist
// in the project's namespace.
func (v *ProjectValidator) ValidateDelete(ctx context.Context, proj *v1alpha1.Project) (admission.Warnings, error) {
	ns := proj.Name

	var tools v1alpha1.ToolList
	if err := v.List(ctx, &tools, client.InNamespace(ns)); err != nil {
		return nil, fmt.Errorf("listing Tools: %w", err)
	}
	if len(tools.Items) > 0 {
		return nil, fmt.Errorf("cannot delete Project %s: %d Tool(s) still exist in namespace", proj.Name, len(tools.Items))
	}

	var cps v1alpha1.CredentialProviderList
	if err := v.List(ctx, &cps, client.InNamespace(ns)); err != nil {
		return nil, fmt.Errorf("listing CredentialProviders: %w", err)
	}
	if len(cps.Items) > 0 {
		return nil, fmt.Errorf("cannot delete Project %s: %d CredentialProvider(s) still exist in namespace", proj.Name, len(cps.Items))
	}

	var agents v1alpha1.AgentList
	if err := v.List(ctx, &agents, client.InNamespace(ns)); err != nil {
		return nil, fmt.Errorf("listing Agents: %w", err)
	}
	if len(agents.Items) > 0 {
		return nil, fmt.Errorf("cannot delete Project %s: %d Agent(s) still exist in namespace", proj.Name, len(agents.Items))
	}

	return nil, nil
}
