package webhook_test

import (
	"context"
	"testing"

	v1alpha1 "github.com/mongodb/mycelium/api/v1alpha1"
	"github.com/mongodb/mycelium/internal/webhook"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func oauthCredentialProvider(name, namespace string) *v1alpha1.CredentialProvider {
	return &v1alpha1.CredentialProvider{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: v1alpha1.CredentialProviderSpec{
			OAuth: &v1alpha1.OAuthProviderSpec{
				ClientID: "client-id",
				ClientSecretRef: corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "secret"},
					Key:                  "client-secret",
				},
				Discovery: v1alpha1.OAuthDiscovery{
					DiscoveryURL: "https://accounts.google.com/.well-known/openid-configuration",
				},
			},
		},
	}
}

func apiKeyCredentialProvider(name, namespace string) *v1alpha1.CredentialProvider {
	return &v1alpha1.CredentialProvider{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: v1alpha1.CredentialProviderSpec{
			APIKey: &v1alpha1.APIKeyProviderSpec{
				APIKeySecretRef: corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "secret"},
					Key:                  "api-key",
				},
			},
		},
	}
}

func baseTool(name, namespace string) *v1alpha1.Tool {
	return &v1alpha1.Tool{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: v1alpha1.ToolSpec{
			Description: "test tool",
			Container:   v1alpha1.ToolContainer{Image: "test:latest"},
		},
	}
}

func oauthCredRef(providerName string, scopes ...string) v1alpha1.CredentialBinding {
	return v1alpha1.CredentialBinding{
		OAuth: &v1alpha1.OAuthCredentialBinding{
			ProviderRef: corev1.LocalObjectReference{Name: providerName},
			Scopes:      scopes,
		},
	}
}

func apiKeyCredRef(providerName string) v1alpha1.CredentialBinding {
	return v1alpha1.CredentialBinding{
		APIKey: &v1alpha1.APIKeyCredentialBinding{
			ProviderRef: corev1.LocalObjectReference{Name: providerName},
		},
	}
}

// --- CREATE ---

func TestToolValidator_CreateAllowsInManagedNamespace(t *testing.T) {
	scheme := newScheme(t)
	require.NoError(t, corev1.AddToScheme(scheme))

	tool := baseTool("list-repos", "acme")
	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(managedNamespace("acme"), readyProject()).Build()

	v := &webhook.ToolValidator{Client: cl}
	_, err := v.ValidateCreate(context.Background(), tool)
	assert.NoError(t, err)
}

func TestToolValidator_CreateRejectsWhenProjectNotFound(t *testing.T) {
	scheme := newScheme(t)
	require.NoError(t, corev1.AddToScheme(scheme))

	tool := baseTool("list-repos", "acme")
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	v := &webhook.ToolValidator{Client: cl}
	_, err := v.ValidateCreate(context.Background(), tool)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestToolValidator_CreateRejectsWhenProjectDeleting(t *testing.T) {
	scheme := newScheme(t)
	require.NoError(t, corev1.AddToScheme(scheme))

	tool := baseTool("list-repos", "acme")
	proj := readyProject()
	now := metav1.Now()
	proj.DeletionTimestamp = &now
	proj.Finalizers = []string{"mycelium.io/project-cleanup"}

	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(managedNamespace("acme"), proj).Build()

	v := &webhook.ToolValidator{Client: cl}
	_, err := v.ValidateCreate(context.Background(), tool)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "being deleted")
}

// --- CREATE: credential ref validation ---

func TestToolValidator_CreateAllowsWithValidOAuthRef(t *testing.T) {
	scheme := newScheme(t)
	require.NoError(t, corev1.AddToScheme(scheme))

	tool := baseTool("list-repos", "acme")
	tool.Spec.Credentials = []v1alpha1.CredentialBinding{oauthCredRef("github", "repo")}

	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(managedNamespace("acme"), readyProject(), oauthCredentialProvider("github", "acme")).Build()

	v := &webhook.ToolValidator{Client: cl}
	_, err := v.ValidateCreate(context.Background(), tool)
	assert.NoError(t, err)
}

func TestToolValidator_CreateRejectsWhenOAuthRefNotFound(t *testing.T) {
	scheme := newScheme(t)
	require.NoError(t, corev1.AddToScheme(scheme))

	tool := baseTool("list-repos", "acme")
	tool.Spec.Credentials = []v1alpha1.CredentialBinding{oauthCredRef("github", "repo")}

	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(managedNamespace("acme"), readyProject()).Build()

	v := &webhook.ToolValidator{Client: cl}
	_, err := v.ValidateCreate(context.Background(), tool)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CredentialProvider github not found")
}

func TestToolValidator_CreateRejectsWhenOAuthRefIsAPIKey(t *testing.T) {
	scheme := newScheme(t)
	require.NoError(t, corev1.AddToScheme(scheme))

	tool := baseTool("list-repos", "acme")
	tool.Spec.Credentials = []v1alpha1.CredentialBinding{oauthCredRef("stripe", "repo")}

	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(managedNamespace("acme"), readyProject(), apiKeyCredentialProvider("stripe", "acme")).Build()

	v := &webhook.ToolValidator{Client: cl}
	_, err := v.ValidateCreate(context.Background(), tool)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not an OAuth provider")
}

func TestToolValidator_CreateRejectsWhenAPIKeyRefIsOAuth(t *testing.T) {
	scheme := newScheme(t)
	require.NoError(t, corev1.AddToScheme(scheme))

	tool := baseTool("list-repos", "acme")
	tool.Spec.Credentials = []v1alpha1.CredentialBinding{apiKeyCredRef("github")}

	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(managedNamespace("acme"), readyProject(), oauthCredentialProvider("github", "acme")).Build()

	v := &webhook.ToolValidator{Client: cl}
	_, err := v.ValidateCreate(context.Background(), tool)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not an API key provider")
}

func TestToolValidator_CreateRejectsWhenCredentialProviderDeleting(t *testing.T) {
	scheme := newScheme(t)
	require.NoError(t, corev1.AddToScheme(scheme))

	tool := baseTool("list-repos", "acme")
	tool.Spec.Credentials = []v1alpha1.CredentialBinding{oauthCredRef("github", "repo")}

	cp := oauthCredentialProvider("github", "acme")
	now := metav1.Now()
	cp.DeletionTimestamp = &now
	cp.Finalizers = []string{"mycelium.io/cleanup"}

	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(managedNamespace("acme"), readyProject(), cp).Build()

	v := &webhook.ToolValidator{Client: cl}
	_, err := v.ValidateCreate(context.Background(), tool)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "being deleted")
}

func TestToolValidator_CreateAllowsWithBothOAuthAndAPIKey(t *testing.T) {
	scheme := newScheme(t)
	require.NoError(t, corev1.AddToScheme(scheme))

	tool := baseTool("list-repos", "acme")
	tool.Spec.Credentials = []v1alpha1.CredentialBinding{
		oauthCredRef("github", "repo"),
		apiKeyCredRef("stripe"),
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(
			managedNamespace("acme"), readyProject(),
			oauthCredentialProvider("github", "acme"),
			apiKeyCredentialProvider("stripe", "acme"),
		).Build()

	v := &webhook.ToolValidator{Client: cl}
	_, err := v.ValidateCreate(context.Background(), tool)
	assert.NoError(t, err)
}

// --- UPDATE ---

func TestToolValidator_UpdateRevalidatesCredentialRefs(t *testing.T) {
	scheme := newScheme(t)
	require.NoError(t, corev1.AddToScheme(scheme))

	tool := baseTool("list-repos", "acme")
	tool.Spec.Credentials = []v1alpha1.CredentialBinding{oauthCredRef("nonexistent", "repo")}

	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(managedNamespace("acme"), readyProject()).Build()

	v := &webhook.ToolValidator{Client: cl}
	_, err := v.ValidateUpdate(context.Background(), baseTool("list-repos", "acme"), tool)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// --- DELETE ---

func newClientWithAgentIndex(t *testing.T, scheme *runtime.Scheme, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithIndex(&v1alpha1.Agent{}, "spec.tools.refs", func(obj client.Object) []string {
			agent := obj.(*v1alpha1.Agent)
			var refs []string
			for _, tr := range agent.Spec.Tools {
				refs = append(refs, tr.Ref.Name)
			}
			return refs
		}).
		Build()
}

func TestToolValidator_DeleteAllowsWhenNoDependents(t *testing.T) {
	scheme := newScheme(t)

	tool := baseTool("list-repos", "acme")
	cl := newClientWithAgentIndex(t, scheme)

	v := &webhook.ToolValidator{Client: cl}
	_, err := v.ValidateDelete(context.Background(), tool)
	assert.NoError(t, err)
}

func TestToolValidator_DeleteRejectsWithDependentAgent(t *testing.T) {
	scheme := newScheme(t)

	tool := baseTool("list-repos", "acme")
	agent := &v1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: "github-assistant", Namespace: "acme"},
		Spec: v1alpha1.AgentSpec{
			Description: "GitHub agent",
			Tools: []v1alpha1.ToolRef{
				{Ref: corev1.LocalObjectReference{Name: "list-repos"}},
			},
			Container: v1alpha1.AgentContainer{Image: "agent:latest"},
		},
	}
	cl := newClientWithAgentIndex(t, scheme, agent)

	v := &webhook.ToolValidator{Client: cl}
	_, err := v.ValidateDelete(context.Background(), tool)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "1 Agent(s)")
}
