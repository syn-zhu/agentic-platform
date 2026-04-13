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
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func managedNamespace(name string) *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
		},
	}
}

func readyProject() *v1alpha1.Project {
	return &v1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: "acme"},
		Spec: v1alpha1.ProjectSpec{
			UserVerifierURL:  "https://app.acme.com/verify",
			IdentityProvider: v1alpha1.IdentityProviderConfig{Issuer: "https://accounts.google.com", Audiences: []string{"acme"}},
		},
		Status: v1alpha1.ProjectStatus{
			NamespaceRef: &corev1.LocalObjectReference{Name: "acme"},
		},
	}
}

// --- CREATE/UPDATE ---

func TestCredentialProviderValidator_CreateAllowsInManagedNamespace(t *testing.T) {
	scheme := newScheme(t)
	require.NoError(t, corev1.AddToScheme(scheme))
	cp := &v1alpha1.CredentialProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "github", Namespace: "acme"},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(managedNamespace("acme"), readyProject()).Build()

	v := &webhook.CredentialProviderValidator{Client: cl}
	err := v.ValidateCreate(context.Background(), cp)
	assert.NoError(t, err)
}

func TestCredentialProviderValidator_CreateRejectsWhenProjectNotFound(t *testing.T) {
	scheme := newScheme(t)
	require.NoError(t, corev1.AddToScheme(scheme))
	cp := &v1alpha1.CredentialProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "github", Namespace: "acme"},
	}
	// No Project exists
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	v := &webhook.CredentialProviderValidator{Client: cl}
	err := v.ValidateCreate(context.Background(), cp)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestCredentialProviderValidator_CreateRejectsWhenNamespaceNotReady(t *testing.T) {
	scheme := newScheme(t)
	require.NoError(t, corev1.AddToScheme(scheme))
	cp := &v1alpha1.CredentialProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "github", Namespace: "acme"},
	}
	// Project exists but NamespaceRef not yet set
	proj := &v1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: "acme"},
		Spec: v1alpha1.ProjectSpec{
			UserVerifierURL:  "https://app.acme.com/verify",
			IdentityProvider: v1alpha1.IdentityProviderConfig{Issuer: "https://accounts.google.com", Audiences: []string{"acme"}},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(managedNamespace("acme"), proj).
		WithStatusSubresource(proj).Build()

	v := &webhook.CredentialProviderValidator{Client: cl}
	err := v.ValidateCreate(context.Background(), cp)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not yet provisioned")
}

func TestCredentialProviderValidator_CreateRejectsWhenProjectDeleting(t *testing.T) {
	scheme := newScheme(t)
	require.NoError(t, corev1.AddToScheme(scheme))
	cp := &v1alpha1.CredentialProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "github", Namespace: "acme"},
	}
	proj := readyProject()
	now := metav1.Now()
	proj.DeletionTimestamp = &now
	proj.Finalizers = []string{"mycelium.io/project-cleanup"}

	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(managedNamespace("acme"), proj).Build()

	v := &webhook.CredentialProviderValidator{Client: cl}
	err := v.ValidateCreate(context.Background(), cp)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "being deleted")
}

// --- DELETE ---

func TestCredentialProviderValidator_DeleteAllowsWhenNoDependents(t *testing.T) {
	scheme := newScheme(t)
	cp := &v1alpha1.CredentialProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "github", Namespace: "acme"},
	}
	cl := newClientWithIndexes(t, scheme)

	v := &webhook.CredentialProviderValidator{Client: cl}
	err := v.ValidateDelete(context.Background(), cp)
	assert.NoError(t, err)
}

func TestCredentialProviderValidator_DeleteRejectsWithDependentOAuth(t *testing.T) {
	scheme := newScheme(t)
	cp := &v1alpha1.CredentialProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "github", Namespace: "acme"},
	}
	tool := &v1alpha1.Tool{
		ObjectMeta: metav1.ObjectMeta{
			Name: "list-repos", Namespace: "acme",
		},
		Spec: v1alpha1.ToolSpec{
			Description: "d",
			Container:   v1alpha1.ToolContainer{Image: "i"},
			Credentials: &v1alpha1.ToolCredentials{
				OAuth: &v1alpha1.OAuthCredentialRef{
					ProviderRef: corev1.LocalObjectReference{Name: "github"},
					Scopes:      []string{"repo"},
				},
			},
		},
	}
	cl := newClientWithIndexes(t, scheme, tool)

	v := &webhook.CredentialProviderValidator{Client: cl}
	err := v.ValidateDelete(context.Background(), cp)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "1 Tool(s)")
}

func TestCredentialProviderValidator_DeleteRejectsWithDependentAPIKey(t *testing.T) {
	scheme := newScheme(t)
	cp := &v1alpha1.CredentialProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "stripe", Namespace: "acme"},
	}
	tool := &v1alpha1.Tool{
		ObjectMeta: metav1.ObjectMeta{
			Name: "charge", Namespace: "acme",
		},
		Spec: v1alpha1.ToolSpec{
			Description: "d",
			Container:   v1alpha1.ToolContainer{Image: "i"},
			Credentials: &v1alpha1.ToolCredentials{
				APIKeys: []v1alpha1.APIKeyCredentialRef{
					{ProviderRef: corev1.LocalObjectReference{Name: "stripe"}},
				},
			},
		},
	}
	cl := newClientWithIndexes(t, scheme, tool)

	v := &webhook.CredentialProviderValidator{Client: cl}
	err := v.ValidateDelete(context.Background(), cp)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "1 Tool(s)")
}

