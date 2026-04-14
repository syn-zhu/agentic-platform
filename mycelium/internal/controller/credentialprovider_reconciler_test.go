package controller_test

import (
	"context"
	"testing"

	v1alpha1 "github.com/mongodb/mycelium/api/v1alpha1"
	"github.com/mongodb/mycelium/internal/controller"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func newOAuthCredentialProvider() *v1alpha1.CredentialProvider {
	return &v1alpha1.CredentialProvider{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "github",
			Namespace:  "tenant-a",
			Finalizers: []string{controller.CredentialProviderFinalizer},
		},
		Spec: v1alpha1.CredentialProviderSpec{
			OAuth: &v1alpha1.OAuthProviderSpec{
				ClientID: "Iv1.abc123",
				ClientSecretRef: corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "github-oauth-secret"},
					Key:                  "client-secret",
				},
				Discovery: v1alpha1.OAuthDiscovery{
					AuthorizationServerMetadata: &v1alpha1.OAuthAuthorizationServerMetadata{
						Issuer:                "https://github.com",
						AuthorizationEndpoint: "https://github.com/login/oauth/authorize",
						TokenEndpoint:         "https://github.com/login/oauth/access_token",
					},
				},
			},
		},
	}
}

func cpProject() *v1alpha1.Project {
	return &v1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: "tenant-a"},
		Spec: v1alpha1.ProjectSpec{
			UserVerifierURL:  "https://app.acme.com/verify",
			IdentityProvider: v1alpha1.IdentityProviderConfig{Issuer: "https://accounts.google.com", Audiences: []string{"acme"}},
		},
	}
}

func oauthSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "github-oauth-secret", Namespace: "tenant-a"},
		Data: map[string][]byte{
			"client-secret": []byte("secret-value"),
		},
	}
}

// --- Happy path ---

func TestCredentialProviderReconciler_SetsReadyCondition(t *testing.T) {
	scheme := newScheme(t)
	cp := newOAuthCredentialProvider()
	proj := cpProject()
	secret := oauthSecret()
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cp, proj, secret).
		WithStatusSubresource(cp).Build()

	r := &controller.CredentialProviderReconciler{Client: cl, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "github", Namespace: "tenant-a"},
	})
	require.NoError(t, err)

	var updated v1alpha1.CredentialProvider
	err = cl.Get(context.Background(), types.NamespacedName{Name: "github", Namespace: "tenant-a"}, &updated)
	require.NoError(t, err)

	projValid := findCondition(updated.Status.Conditions, "ProjectValid")
	secretValid := findCondition(updated.Status.Conditions, "SecretValid")
	ready := findCondition(updated.Status.Conditions, "Ready")

	require.NotNil(t, projValid)
	require.NotNil(t, secretValid)
	require.NotNil(t, ready)

	assert.Equal(t, metav1.ConditionTrue, projValid.Status)
	assert.Equal(t, metav1.ConditionTrue, secretValid.Status)
	assert.Equal(t, metav1.ConditionTrue, ready.Status)
}

func TestCredentialProviderReconciler_AddsFinalizer(t *testing.T) {
	scheme := newScheme(t)
	cp := &v1alpha1.CredentialProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "github", Namespace: "tenant-a"},
		Spec:       newOAuthCredentialProvider().Spec,
	}
	proj := cpProject()
	secret := oauthSecret()
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cp, proj, secret).
		WithStatusSubresource(cp).Build()

	r := &controller.CredentialProviderReconciler{Client: cl, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "github", Namespace: "tenant-a"},
	})
	require.NoError(t, err)

	var updated v1alpha1.CredentialProvider
	err = cl.Get(context.Background(), types.NamespacedName{Name: "github", Namespace: "tenant-a"}, &updated)
	require.NoError(t, err)
	assert.Contains(t, updated.Finalizers, controller.CredentialProviderFinalizer)
}

// --- Project validation ---

func TestCredentialProviderReconciler_ProjectNotFound(t *testing.T) {
	scheme := newScheme(t)
	cp := newOAuthCredentialProvider()
	secret := oauthSecret()
	// No Project
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cp, secret).
		WithStatusSubresource(cp).Build()

	r := &controller.CredentialProviderReconciler{Client: cl, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "github", Namespace: "tenant-a"},
	})
	require.NoError(t, err)

	var updated v1alpha1.CredentialProvider
	err = cl.Get(context.Background(), types.NamespacedName{Name: "github", Namespace: "tenant-a"}, &updated)
	require.NoError(t, err)

	projValid := findCondition(updated.Status.Conditions, "ProjectValid")
	ready := findCondition(updated.Status.Conditions, "Ready")

	require.NotNil(t, projValid)
	assert.Equal(t, metav1.ConditionFalse, projValid.Status)
	assert.Equal(t, "ProjectNotFound", projValid.Reason)

	require.NotNil(t, ready)
	assert.Equal(t, metav1.ConditionFalse, ready.Status)
	assert.Equal(t, "ProjectInvalid", ready.Reason)
}

// --- Secret validation ---

func TestCredentialProviderReconciler_SecretNotFound(t *testing.T) {
	scheme := newScheme(t)
	cp := newOAuthCredentialProvider()
	proj := cpProject()
	// No Secret
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cp, proj).
		WithStatusSubresource(cp).Build()

	r := &controller.CredentialProviderReconciler{Client: cl, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "github", Namespace: "tenant-a"},
	})
	require.NoError(t, err)

	var updated v1alpha1.CredentialProvider
	err = cl.Get(context.Background(), types.NamespacedName{Name: "github", Namespace: "tenant-a"}, &updated)
	require.NoError(t, err)

	secretValid := findCondition(updated.Status.Conditions, "SecretValid")
	ready := findCondition(updated.Status.Conditions, "Ready")

	require.NotNil(t, secretValid)
	assert.Equal(t, metav1.ConditionFalse, secretValid.Status)
	assert.Equal(t, "SecretNotFound", secretValid.Reason)
	assert.Contains(t, secretValid.Message, "github-oauth-secret")

	require.NotNil(t, ready)
	assert.Equal(t, metav1.ConditionFalse, ready.Status)
	assert.Equal(t, "SecretMissing", ready.Reason)
}

// --- Deletion ---

func TestCredentialProviderReconciler_DeletionRemovesFinalizer(t *testing.T) {
	scheme := newScheme(t)
	cp := newOAuthCredentialProvider()
	now := metav1.Now()
	cp.DeletionTimestamp = &now

	cl := newClientWithIndexes(t, scheme, cp)

	r := &controller.CredentialProviderReconciler{Client: cl, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "github", Namespace: "tenant-a"},
	})
	require.NoError(t, err)

	var updated v1alpha1.CredentialProvider
	err = cl.Get(context.Background(), types.NamespacedName{Name: "github", Namespace: "tenant-a"}, &updated)
	assert.True(t, err != nil, "expected object to be deleted after finalizer removal")
}

func TestCredentialProviderReconciler_DeletionRequeuesWithDependentTools(t *testing.T) {
	scheme := newScheme(t)
	cp := newOAuthCredentialProvider()
	now := metav1.Now()
	cp.DeletionTimestamp = &now

	tool := &v1alpha1.Tool{
		ObjectMeta: metav1.ObjectMeta{Name: "list-repos", Namespace: "tenant-a"},
		Spec: v1alpha1.ToolSpec{
			Description: "d",
			Container:   v1alpha1.ToolContainer{Image: "i"},
			Credentials: []v1alpha1.CredentialBinding{
				{
					OAuth: &v1alpha1.OAuthCredentialBinding{
						ProviderRef: corev1.LocalObjectReference{Name: "github"},
						Scopes:      []string{"repo"},
					},
				},
			},
		},
	}

	cl := newClientWithIndexes(t, scheme, cp, tool)

	r := &controller.CredentialProviderReconciler{Client: cl, Scheme: scheme}
	result, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "github", Namespace: "tenant-a"},
	})
	require.NoError(t, err)
	assert.NotZero(t, result.RequeueAfter, "expected requeue when dependent tools exist")

	var updated v1alpha1.CredentialProvider
	err = cl.Get(context.Background(), types.NamespacedName{Name: "github", Namespace: "tenant-a"}, &updated)
	require.NoError(t, err)
	assert.Contains(t, updated.Finalizers, controller.CredentialProviderFinalizer)
}

func TestCredentialProviderReconciler_NotFound(t *testing.T) {
	scheme := newScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	r := &controller.CredentialProviderReconciler{Client: cl, Scheme: scheme}
	result, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "gone", Namespace: "tenant-a"},
	})
	require.NoError(t, err)
	assert.False(t, result.Requeue)
}
