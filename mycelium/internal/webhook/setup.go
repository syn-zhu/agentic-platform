package webhook

import (
	ctrl "sigs.k8s.io/controller-runtime"
)

// SetupWebhooks registers all validating webhooks with the manager.
func SetupWebhooks(mgr ctrl.Manager) error {
	cl := mgr.GetClient()

	if err := (&ProjectValidator{Client: cl}).SetupWebhookWithManager(mgr); err != nil {
		return err
	}
	if err := (&AgentValidator{Client: cl}).SetupWebhookWithManager(mgr); err != nil {
		return err
	}
	if err := (&ToolValidator{Client: cl}).SetupWebhookWithManager(mgr); err != nil {
		return err
	}
	if err := (&CredentialProviderValidator{Client: cl}).SetupWebhookWithManager(mgr); err != nil {
		return err
	}

	return nil
}
