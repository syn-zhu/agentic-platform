package webhook

import (
	corev1 "k8s.io/api/core/v1"
	v1alpha1 "mycelium.io/mycelium/api/v1alpha1"
	agentwebhook "mycelium.io/mycelium/internal/webhook/agent"
	cpwebhook "mycelium.io/mycelium/internal/webhook/credentialprovider"
	projectwebhook "mycelium.io/mycelium/internal/webhook/project"
	secretwebhook "mycelium.io/mycelium/internal/webhook/secret"
	toolwebhook "mycelium.io/mycelium/internal/webhook/tool"
	ctrl "sigs.k8s.io/controller-runtime"
)

// SetupWebhooks registers all validating and defaulting webhooks with the manager.
func SetupWebhooks(mgr ctrl.Manager) error {
	cl := mgr.GetClient()

	if err := ctrl.NewWebhookManagedBy(mgr, &v1alpha1.MyceliumEcosystem{}).
		WithDefaulter(&projectwebhook.Defaulter{}).
		WithValidator(&projectwebhook.Validator{Client: cl}).
		Complete(); err != nil {
		return err
	}

	if err := ctrl.NewWebhookManagedBy(mgr, &v1alpha1.MyceliumAgent{}).
		WithDefaulter(&agentwebhook.Defaulter{}).
		WithValidator(&agentwebhook.Validator{Client: cl}).
		Complete(); err != nil {
		return err
	}

	if err := ctrl.NewWebhookManagedBy(mgr, &v1alpha1.MyceliumTool{}).
		WithDefaulter(&toolwebhook.Defaulter{}).
		WithValidator(&toolwebhook.Validator{Client: cl}).
		Complete(); err != nil {
		return err
	}

	if err := ctrl.NewWebhookManagedBy(mgr, &v1alpha1.MyceliumCredentialProvider{}).
		WithDefaulter(&cpwebhook.Defaulter{}).
		WithValidator(&cpwebhook.Validator{Client: cl}).
		Complete(); err != nil {
		return err
	}

	if err := ctrl.NewWebhookManagedBy(mgr, &corev1.Secret{}).
		WithValidator(&secretwebhook.Validator{Client: cl}).
		Complete(); err != nil {
		return err
	}

	return nil
}
