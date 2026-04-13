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
	knservingv1 "knative.dev/serving/pkg/apis/serving/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func newTool() *v1alpha1.Tool {
	return &v1alpha1.Tool{
		ObjectMeta: metav1.ObjectMeta{Name: "list-repos", Namespace: "tenant-a"},
		Spec: v1alpha1.ToolSpec{
			Description: "List GitHub repos for an org.",
			Container:   v1alpha1.ToolContainer{Image: "tenant-a/tool-list-repos:latest"},
			Credentials: &v1alpha1.ToolCredentials{
				OAuth: &v1alpha1.OAuthCredentialRef{
					ProviderRef: corev1.LocalObjectReference{Name: "github"},
					Scopes:      []string{"repo"},
				},
			},
		},
	}
}

func TestToolReconciler_CreatesKnativeService(t *testing.T) {
	scheme := newScheme(t)
	tool := newTool()
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tool).
		WithStatusSubresource(tool).Build()

	r := &controller.ToolReconciler{Client: cl, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "list-repos", Namespace: "tenant-a"},
	})
	require.NoError(t, err)

	// Verify Knative Service was created
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
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tool).
		WithStatusSubresource(tool).Build()

	r := &controller.ToolReconciler{Client: cl, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "list-repos", Namespace: "tenant-a"},
	})
	require.NoError(t, err)

	var updated v1alpha1.Tool
	err = cl.Get(context.Background(), types.NamespacedName{Name: "list-repos", Namespace: "tenant-a"}, &updated)
	require.NoError(t, err)
	require.NotNil(t, updated.Status.ServiceRef)
	assert.Equal(t, "list-repos", updated.Status.ServiceRef.Name)
}

func TestToolReconciler_AddsFinalizer(t *testing.T) {
	scheme := newScheme(t)
	tool := newTool()
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tool).
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
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tool).
		WithStatusSubresource(tool).Build()

	r := &controller.ToolReconciler{Client: cl, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "list-repos", Namespace: "tenant-a"},
	})
	require.NoError(t, err)

	var updated v1alpha1.Tool
	err = cl.Get(context.Background(), types.NamespacedName{Name: "list-repos", Namespace: "tenant-a"}, &updated)
	require.NoError(t, err)
	require.NotEmpty(t, updated.Status.Conditions)
	ready := findCondition(updated.Status.Conditions, "Ready")
	require.NotNil(t, ready)
	assert.Equal(t, metav1.ConditionTrue, ready.Status)
}

func TestToolReconciler_DeletionRemovesFinalizer(t *testing.T) {
	scheme := newScheme(t)
	tool := newTool()
	tool.Finalizers = []string{controller.ToolFinalizer}
	now := metav1.Now()
	tool.DeletionTimestamp = &now

	// No agents referencing this tool
	cl := newClientWithIndexes(t, scheme, tool)

	r := &controller.ToolReconciler{Client: cl, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "list-repos", Namespace: "tenant-a"},
	})
	require.NoError(t, err)

	// Object should be deleted (fake client removes when finalizer cleared + DeletionTimestamp set)
	var updated v1alpha1.Tool
	err = cl.Get(context.Background(), types.NamespacedName{Name: "list-repos", Namespace: "tenant-a"}, &updated)
	assert.True(t, err != nil, "expected tool to be deleted after finalizer removal")
}

func TestToolReconciler_DeletionRequeuesWithDependentAgents(t *testing.T) {
	scheme := newScheme(t)
	tool := newTool()
	tool.Finalizers = []string{controller.ToolFinalizer}
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

	// Tool should still have its finalizer
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
