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
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func newAgent() *v1alpha1.Agent {
	return &v1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "github-assistant",
			Namespace:  "acme",
			Finalizers: []string{controller.AgentFinalizer},
		},
		Spec: v1alpha1.AgentSpec{
			Description: "GitHub agent",
			Tools: []v1alpha1.ToolRef{
				{Ref: corev1.LocalObjectReference{Name: "list-repos"}},
			},
			Container: v1alpha1.AgentContainer{Image: "acme/gh:latest"},
		},
	}
}

func TestAgentReconciler_CreatesServiceAccount(t *testing.T) {
	scheme := newScheme(t)
	agent := newAgent()
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(agent).
		WithStatusSubresource(agent).Build()

	r := &controller.AgentReconciler{Client: cl, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "github-assistant", Namespace: "acme"},
	})
	require.NoError(t, err)

	// ServiceAccount created in the agent's namespace
	var sa corev1.ServiceAccount
	err = cl.Get(context.Background(), types.NamespacedName{Name: "github-assistant", Namespace: "acme"}, &sa)
	require.NoError(t, err)
	assert.Equal(t, "mycelium-controller", sa.Labels["app.kubernetes.io/managed-by"])
	assert.Equal(t, "github-assistant", sa.Annotations["mycelium.io/agent"])

	// Status ref points to it
	var updated v1alpha1.Agent
	err = cl.Get(context.Background(), types.NamespacedName{Name: "github-assistant", Namespace: "acme"}, &updated)
	require.NoError(t, err)
	require.NotNil(t, updated.Status.ServiceAccount)
	assert.Equal(t, "github-assistant", updated.Status.ServiceAccount.Ref)
}

func TestAgentReconciler_SetsReadyCondition(t *testing.T) {
	scheme := newScheme(t)
	agent := newAgent()
	// Tool must exist for ToolsValid=True → Ready=True
	tool := &v1alpha1.Tool{
		ObjectMeta: metav1.ObjectMeta{Name: "list-repos", Namespace: "acme"},
		Spec: v1alpha1.ToolSpec{
			Description: "d", Container: v1alpha1.ToolContainer{Image: "i"},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(agent, tool).
		WithStatusSubresource(agent).Build()

	r := &controller.AgentReconciler{Client: cl, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "github-assistant", Namespace: "acme"},
	})
	require.NoError(t, err)

	var updated v1alpha1.Agent
	err = cl.Get(context.Background(), types.NamespacedName{Name: "github-assistant", Namespace: "acme"}, &updated)
	require.NoError(t, err)

	saReady := findCondition(updated.Status.Conditions, "ServiceAccountReady")
	ready := findCondition(updated.Status.Conditions, "Ready")

	require.NotNil(t, saReady)
	require.NotNil(t, ready)
	assert.Equal(t, metav1.ConditionTrue, saReady.Status)
	assert.Equal(t, metav1.ConditionTrue, ready.Status)
}

func TestAgentReconciler_AddsFinalizer(t *testing.T) {
	scheme := newScheme(t)
	agent := &v1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: "github-assistant", Namespace: "acme"},
		Spec: v1alpha1.AgentSpec{
			Description: "GitHub agent",
			Tools:       []v1alpha1.ToolRef{{Ref: corev1.LocalObjectReference{Name: "list-repos"}}},
			Container:   v1alpha1.AgentContainer{Image: "acme/gh:latest"},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(agent).
		WithStatusSubresource(agent).Build()

	r := &controller.AgentReconciler{Client: cl, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "github-assistant", Namespace: "acme"},
	})
	require.NoError(t, err)

	var updated v1alpha1.Agent
	err = cl.Get(context.Background(), types.NamespacedName{Name: "github-assistant", Namespace: "acme"}, &updated)
	require.NoError(t, err)
	assert.Contains(t, updated.Finalizers, controller.AgentFinalizer)
}

func TestAgentReconciler_DeletionRemovesFinalizer(t *testing.T) {
	scheme := newScheme(t)
	agent := newAgent()
	now := metav1.Now()
	agent.DeletionTimestamp = &now

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(agent).
		WithStatusSubresource(agent).Build()

	r := &controller.AgentReconciler{Client: cl, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "github-assistant", Namespace: "acme"},
	})
	require.NoError(t, err)

	// Object should be deleted (fake client deletes when finalizer removed + DeletionTimestamp set)
	var updated v1alpha1.Agent
	err = cl.Get(context.Background(), types.NamespacedName{Name: "github-assistant", Namespace: "acme"}, &updated)
	assert.True(t, err != nil, "expected object to be deleted after finalizer removal")
}

func TestAgentReconciler_NotFound(t *testing.T) {
	scheme := newScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	r := &controller.AgentReconciler{Client: cl, Scheme: scheme}
	result, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "gone", Namespace: "acme"},
	})
	require.NoError(t, err)
	assert.False(t, result.Requeue)
}
