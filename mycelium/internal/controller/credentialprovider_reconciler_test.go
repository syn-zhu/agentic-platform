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
		ObjectMeta: metav1.ObjectMeta{Name: "github", Namespace: "tenant-a"},
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

func TestCredentialProviderReconciler_SetsReadyCondition(t *testing.T) {
	scheme := newScheme(t)
	cp := newOAuthCredentialProvider()
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cp).
		WithStatusSubresource(cp).Build()

	r := &controller.CredentialProviderReconciler{Client: cl, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "github", Namespace: "tenant-a"},
	})
	require.NoError(t, err)

	var updated v1alpha1.CredentialProvider
	err = cl.Get(context.Background(), types.NamespacedName{Name: "github", Namespace: "tenant-a"}, &updated)
	require.NoError(t, err)
	require.NotEmpty(t, updated.Status.Conditions)
	assert.Equal(t, "Ready", updated.Status.Conditions[0].Type)
	assert.Equal(t, metav1.ConditionTrue, updated.Status.Conditions[0].Status)
}

func TestCredentialProviderReconciler_AddsFinalizer(t *testing.T) {
	scheme := newScheme(t)
	cp := newOAuthCredentialProvider()
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cp).
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

func TestCredentialProviderReconciler_BlocksDeletionWithDependentTools(t *testing.T) {
	scheme := newScheme(t)
	cp := newOAuthCredentialProvider()
	// Simulate finalizer already added + deletion in progress
	cp.Finalizers = []string{controller.CredentialProviderFinalizer}
	now := metav1.Now()
	cp.DeletionTimestamp = &now

	// A Tool that references this CredentialProvider
	tool := &v1alpha1.Tool{
		ObjectMeta: metav1.ObjectMeta{Name: "list-repos", Namespace: "tenant-a"},
		Spec: v1alpha1.ToolSpec{
			ToolName:    "list_repos",
			Description: "List repos",
			Container:   v1alpha1.ToolContainer{Image: "tools/list-repos:latest"},
			Credentials: &v1alpha1.ToolCredentials{
				OAuth: &v1alpha1.OAuthCredentialRef{
					ProviderRef: corev1.LocalObjectReference{Name: "github"},
					Scopes:      []string{"repo"},
				},
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cp, tool).
		WithStatusSubresource(cp).Build()

	r := &controller.CredentialProviderReconciler{Client: cl, Scheme: scheme}
	result, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "github", Namespace: "tenant-a"},
	})
	require.NoError(t, err)
	// Should requeue — finalizer NOT removed because dependent tool exists
	assert.True(t, result.RequeueAfter > 0)

	var updated v1alpha1.CredentialProvider
	err = cl.Get(context.Background(), types.NamespacedName{Name: "github", Namespace: "tenant-a"}, &updated)
	require.NoError(t, err)
	assert.Contains(t, updated.Finalizers, controller.CredentialProviderFinalizer)
}

func TestCredentialProviderReconciler_AllowsDeletionWithNoDependents(t *testing.T) {
	scheme := newScheme(t)
	cp := newOAuthCredentialProvider()
	cp.Finalizers = []string{controller.CredentialProviderFinalizer}
	now := metav1.Now()
	cp.DeletionTimestamp = &now

	// No tools referencing this provider
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cp).
		WithStatusSubresource(cp).Build()

	r := &controller.CredentialProviderReconciler{Client: cl, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "github", Namespace: "tenant-a"},
	})
	require.NoError(t, err)

	// Object should be deleted (fake client deletes when finalizer removed + DeletionTimestamp set)
	var updated v1alpha1.CredentialProvider
	err = cl.Get(context.Background(), types.NamespacedName{Name: "github", Namespace: "tenant-a"}, &updated)
	assert.True(t, err != nil, "expected object to be deleted after finalizer removal")
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
