package controller_test

import (
	"context"
	"testing"

	v1alpha1 "mycelium.io/mycelium/api/v1alpha1"
	"mycelium.io/mycelium/internal/controller"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	knservingv1 "knative.dev/serving/pkg/apis/serving/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func newTool() *v1alpha1.Tool {
	return &v1alpha1.Tool{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "list-repos",
			Namespace:  "tenant-a",
			Finalizers: []string{controller.ToolFinalizer},
		},
		Spec: v1alpha1.ToolSpec{
			Description: "List GitHub repos for an org.",
			Container:   v1alpha1.ToolContainer{Image: "tenant-a/tool-list-repos:latest"},
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
}

func toolProject() *v1alpha1.Project {
	return &v1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: "tenant-a"},
		Spec: v1alpha1.ProjectSpec{
			UserVerifierURL:  "https://app.acme.com/verify",
			IdentityProvider: v1alpha1.IdentityProviderConfig{Issuer: "https://accounts.google.com", Audiences: []string{"acme"}},
		},
	}
}

func githubCredentialProvider() *v1alpha1.CredentialProvider {
	return &v1alpha1.CredentialProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "github", Namespace: "tenant-a"},
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

// --- CREATE: happy path ---

func TestToolReconciler_CreatesKnativeService(t *testing.T) {
	scheme := newScheme(t)
	tool := newTool()
	proj := toolProject()
	cp := githubCredentialProvider()
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tool, proj, cp).
		WithStatusSubresource(tool).Build()

	r := &controller.ToolReconciler{Client: cl, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "list-repos", Namespace: "tenant-a"},
	})
	require.NoError(t, err)

	var svc knservingv1.Service
	err = cl.Get(context.Background(), types.NamespacedName{Name: "list-repos", Namespace: "tenant-a"}, &svc)
	require.NoError(t, err)
	assert.Equal(t, "mycelium-controller", svc.Labels["app.kubernetes.io/managed-by"])
	assert.Equal(t, "list-repos", svc.Annotations["mycelium.io/tool"])
	require.NotNil(t, svc.Spec.Template.Spec.ContainerConcurrency)
	assert.Equal(t, int64(1), *svc.Spec.Template.Spec.ContainerConcurrency)
	assert.Equal(t, "kata-fc", *svc.Spec.Template.Spec.RuntimeClassName)
	require.Len(t, svc.Spec.Template.Spec.Containers, 1)
	assert.Equal(t, "tenant-a/tool-list-repos:latest", svc.Spec.Template.Spec.Containers[0].Image)
}

func TestToolReconciler_SetsStatusServiceRef(t *testing.T) {
	scheme := newScheme(t)
	tool := newTool()
	proj := toolProject()
	cp := githubCredentialProvider()
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tool, proj, cp).
		WithStatusSubresource(tool).Build()

	r := &controller.ToolReconciler{Client: cl, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "list-repos", Namespace: "tenant-a"},
	})
	require.NoError(t, err)

	var updated v1alpha1.Tool
	err = cl.Get(context.Background(), types.NamespacedName{Name: "list-repos", Namespace: "tenant-a"}, &updated)
	require.NoError(t, err)
	require.NotNil(t, updated.Status.Service)
	assert.Equal(t, "list-repos", updated.Status.Service.Ref)
}

func TestToolReconciler_AddsFinalizer(t *testing.T) {
	scheme := newScheme(t)
	tool := &v1alpha1.Tool{
		ObjectMeta: metav1.ObjectMeta{Name: "list-repos", Namespace: "tenant-a"},
		Spec: v1alpha1.ToolSpec{
			Description: "List GitHub repos for an org.",
			Container:   v1alpha1.ToolContainer{Image: "tenant-a/tool-list-repos:latest"},
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
	proj := toolProject()
	cp := githubCredentialProvider()
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tool, proj, cp).
		WithStatusSubresource(tool).Build()

	r := &controller.ToolReconciler{Client: cl, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "list-repos", Namespace: "tenant-a"},
	})
	require.NoError(t, err)

	var updated v1alpha1.Tool
	err = cl.Get(context.Background(), types.NamespacedName{Name: "list-repos", Namespace: "tenant-a"}, &updated)
	require.NoError(t, err)
	assert.Contains(t, updated.Finalizers, controller.ToolFinalizer)
}

func TestToolReconciler_SetsReadyCondition(t *testing.T) {
	scheme := newScheme(t)
	tool := newTool()
	proj := toolProject()
	cp := githubCredentialProvider()
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tool, proj, cp).
		WithStatusSubresource(tool).Build()

	r := &controller.ToolReconciler{Client: cl, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "list-repos", Namespace: "tenant-a"},
	})
	require.NoError(t, err)

	var updated v1alpha1.Tool
	err = cl.Get(context.Background(), types.NamespacedName{Name: "list-repos", Namespace: "tenant-a"}, &updated)
	require.NoError(t, err)

	require.NotNil(t, updated.Status.Project)
	assert.True(t, updated.Status.Project.IsReady())
	require.NotNil(t, updated.Status.Service)
	assert.True(t, updated.Status.Service.IsReady())
	require.Len(t, updated.Status.CredentialBindings, 1)
	assert.True(t, updated.Status.CredentialBindings[0].IsReady())

	ready := findCondition(updated.Status.Conditions, "Ready")
	require.NotNil(t, ready)
	assert.Equal(t, metav1.ConditionTrue, ready.Status)
}

// --- Project validation ---

func TestToolReconciler_ProjectNotFound(t *testing.T) {
	scheme := newScheme(t)
	tool := newTool()
	cp := githubCredentialProvider()
	// No Project created
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tool, cp).
		WithStatusSubresource(tool).Build()

	r := &controller.ToolReconciler{Client: cl, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "list-repos", Namespace: "tenant-a"},
	})
	require.NoError(t, err)

	var updated v1alpha1.Tool
	err = cl.Get(context.Background(), types.NamespacedName{Name: "list-repos", Namespace: "tenant-a"}, &updated)
	require.NoError(t, err)

	require.NotNil(t, updated.Status.Project)
	assert.False(t, updated.Status.Project.IsReady())
	assert.Equal(t, "NotFound", findCondition(updated.Status.Project.Conditions, "Ready").Reason)

	ready := findCondition(updated.Status.Conditions, "Ready")
	require.NotNil(t, ready)
	assert.Equal(t, metav1.ConditionFalse, ready.Status)
}

// --- Credential validation ---

func TestToolReconciler_CredentialsInvalidWhenProviderMissing(t *testing.T) {
	scheme := newScheme(t)
	tool := newTool()
	proj := toolProject()
	// No CredentialProvider created — ref is dangling
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tool, proj).
		WithStatusSubresource(tool).Build()

	r := &controller.ToolReconciler{Client: cl, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "list-repos", Namespace: "tenant-a"},
	})
	require.NoError(t, err)

	var updated v1alpha1.Tool
	err = cl.Get(context.Background(), types.NamespacedName{Name: "list-repos", Namespace: "tenant-a"}, &updated)
	require.NoError(t, err)

	credsValid := findCondition(updated.Status.Conditions, "CredentialsValid")
	ready := findCondition(updated.Status.Conditions, "Ready")

	require.NotNil(t, credsValid)
	assert.Equal(t, metav1.ConditionFalse, credsValid.Status)
	assert.Equal(t, "InvalidCredentialRef", credsValid.Reason)
	assert.Contains(t, credsValid.Message, "not found")

	require.NotNil(t, ready)
	assert.Equal(t, metav1.ConditionFalse, ready.Status)
	assert.Equal(t, "CredentialsInvalid", ready.Reason)
}

func TestToolReconciler_CredentialsInvalidWhenProviderWrongType(t *testing.T) {
	scheme := newScheme(t)
	tool := newTool()
	proj := toolProject()
	// Create an API key provider where OAuth is expected
	wrongTypeCp := &v1alpha1.CredentialProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "github", Namespace: "tenant-a"},
		Spec: v1alpha1.CredentialProviderSpec{
			APIKey: &v1alpha1.APIKeyProviderSpec{
				APIKeySecretRef: corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "secret"},
					Key:                  "api-key",
				},
			},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tool, proj, wrongTypeCp).
		WithStatusSubresource(tool).Build()

	r := &controller.ToolReconciler{Client: cl, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "list-repos", Namespace: "tenant-a"},
	})
	require.NoError(t, err)

	var updated v1alpha1.Tool
	err = cl.Get(context.Background(), types.NamespacedName{Name: "list-repos", Namespace: "tenant-a"}, &updated)
	require.NoError(t, err)

	credsValid := findCondition(updated.Status.Conditions, "CredentialsValid")
	require.NotNil(t, credsValid)
	assert.Equal(t, metav1.ConditionFalse, credsValid.Status)
	assert.Contains(t, credsValid.Message, "not an OAuth provider")
}

// --- Deletion ---

func TestToolReconciler_DeletionRemovesFinalizer(t *testing.T) {
	scheme := newScheme(t)
	tool := newTool()
	now := metav1.Now()
	tool.DeletionTimestamp = &now

	cl := newClientWithIndexes(t, scheme, tool)

	r := &controller.ToolReconciler{Client: cl, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "list-repos", Namespace: "tenant-a"},
	})
	require.NoError(t, err)

	var updated v1alpha1.Tool
	err = cl.Get(context.Background(), types.NamespacedName{Name: "list-repos", Namespace: "tenant-a"}, &updated)
	assert.True(t, err != nil, "expected tool to be deleted after finalizer removal")
}

func TestToolReconciler_DeletionRequeuesWithDependentAgents(t *testing.T) {
	scheme := newScheme(t)
	tool := newTool()
	now := metav1.Now()
	tool.DeletionTimestamp = &now

	agent := &v1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: "github-assistant", Namespace: "tenant-a"},
		Spec: v1alpha1.AgentSpec{
			Description: "GH agent",
			Tools: []v1alpha1.ToolRef{
				{Ref: corev1.LocalObjectReference{Name: "list-repos"}},
			},
			Container: v1alpha1.AgentContainer{Image: "img"},
		},
	}

	cl := newClientWithIndexes(t, scheme, tool, agent)

	r := &controller.ToolReconciler{Client: cl, Scheme: scheme}
	result, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "list-repos", Namespace: "tenant-a"},
	})
	require.NoError(t, err)
	assert.NotZero(t, result.RequeueAfter, "expected requeue when dependent agents exist")

	var updated v1alpha1.Tool
	err = cl.Get(context.Background(), types.NamespacedName{Name: "list-repos", Namespace: "tenant-a"}, &updated)
	require.NoError(t, err)
	assert.Contains(t, updated.Finalizers, controller.ToolFinalizer)
}

func TestToolReconciler_NotFound(t *testing.T) {
	scheme := newScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	r := &controller.ToolReconciler{Client: cl, Scheme: scheme}
	result, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "gone", Namespace: "tenant-a"},
	})
	require.NoError(t, err)
	assert.False(t, result.Requeue)
}
