package webhook_test

import (
	"context"
	"testing"

	v1alpha1 "mycelium.io/mycelium/api/v1alpha1"
	"mycelium.io/mycelium/internal/webhook"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newTestAgent() *v1alpha1.Agent {
	return &v1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: "github-assistant", Namespace: "acme"},
		Spec: v1alpha1.AgentSpec{
			Description: "GitHub agent",
			Tools: []v1alpha1.ToolRef{
				{Ref: corev1.LocalObjectReference{Name: "list-repos"}},
			},
			Container: v1alpha1.AgentContainer{Image: "acme/gh:latest"},
		},
	}
}

func TestAgentValidator_CreateAllows(t *testing.T) {
	scheme := newScheme(t)
	require.NoError(t, corev1.AddToScheme(scheme))

	tool := &v1alpha1.Tool{
		ObjectMeta: metav1.ObjectMeta{Name: "list-repos", Namespace: "acme"},
		Spec:       v1alpha1.ToolSpec{Description: "d", Container: v1alpha1.ToolContainer{Image: "i"}},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(managedNamespace("acme"), readyProject(), tool).Build()

	v := &webhook.AgentValidator{Client: cl}
	_, err := v.ValidateCreate(context.Background(), newTestAgent())
	assert.NoError(t, err)
}

func TestAgentValidator_CreateRejectsWhenProjectNotFound(t *testing.T) {
	scheme := newScheme(t)
	require.NoError(t, corev1.AddToScheme(scheme))

	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	v := &webhook.AgentValidator{Client: cl}
	_, err := v.ValidateCreate(context.Background(), newTestAgent())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestAgentValidator_CreateRejectsWhenProjectDeleting(t *testing.T) {
	scheme := newScheme(t)
	require.NoError(t, corev1.AddToScheme(scheme))

	proj := readyProject()
	now := metav1.Now()
	proj.DeletionTimestamp = &now
	proj.Finalizers = []string{"mycelium.io/project-cleanup"}

	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(managedNamespace("acme"), proj).Build()

	v := &webhook.AgentValidator{Client: cl}
	_, err := v.ValidateCreate(context.Background(), newTestAgent())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "being deleted")
}

func TestAgentValidator_CreateRejectsWhenToolNotFound(t *testing.T) {
	scheme := newScheme(t)
	require.NoError(t, corev1.AddToScheme(scheme))

	// Project ready but tool doesn't exist
	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(managedNamespace("acme"), readyProject()).Build()

	v := &webhook.AgentValidator{Client: cl}
	_, err := v.ValidateCreate(context.Background(), newTestAgent())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list-repos")
	assert.Contains(t, err.Error(), "not found")
}

func TestAgentValidator_UpdateAllowsValidToolRefs(t *testing.T) {
	scheme := newScheme(t)
	require.NoError(t, corev1.AddToScheme(scheme))

	tool := &v1alpha1.Tool{
		ObjectMeta: metav1.ObjectMeta{Name: "list-repos", Namespace: "acme"},
		Spec:       v1alpha1.ToolSpec{Description: "d", Container: v1alpha1.ToolContainer{Image: "i"}},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(managedNamespace("acme"), readyProject(), tool).Build()

	v := &webhook.AgentValidator{Client: cl}
	_, err := v.ValidateUpdate(context.Background(), newTestAgent(), newTestAgent())
	assert.NoError(t, err)
}

func TestAgentValidator_UpdateRejectsWhenToolNotFound(t *testing.T) {
	scheme := newScheme(t)
	require.NoError(t, corev1.AddToScheme(scheme))

	// Tool doesn't exist
	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(managedNamespace("acme"), readyProject()).Build()

	v := &webhook.AgentValidator{Client: cl}
	_, err := v.ValidateUpdate(context.Background(), newTestAgent(), newTestAgent())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list-repos")
	assert.Contains(t, err.Error(), "not found")
}

func TestAgentValidator_CreateRejectsWhenToolDeleting(t *testing.T) {
	scheme := newScheme(t)
	require.NoError(t, corev1.AddToScheme(scheme))

	tool := &v1alpha1.Tool{
		ObjectMeta: metav1.ObjectMeta{
			Name: "list-repos", Namespace: "acme",
			Finalizers:        []string{"mycelium.io/tool-cleanup"},
			DeletionTimestamp: func() *metav1.Time { now := metav1.Now(); return &now }(),
		},
		Spec: v1alpha1.ToolSpec{Description: "d", Container: v1alpha1.ToolContainer{Image: "i"}},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(managedNamespace("acme"), readyProject(), tool).Build()

	v := &webhook.AgentValidator{Client: cl}
	_, err := v.ValidateCreate(context.Background(), newTestAgent())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list-repos")
	assert.Contains(t, err.Error(), "being deleted")
}
