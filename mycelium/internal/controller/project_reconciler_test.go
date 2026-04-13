package controller_test

import (
	"context"
	"testing"

	v1alpha1 "github.com/mongodb/mycelium/api/v1alpha1"
	"github.com/mongodb/mycelium/internal/controller"

	agwv1alpha1 "github.com/agentgateway/agentgateway/controller/api/v1alpha1/agentgateway"
	"github.com/agentgateway/agentgateway/controller/api/v1alpha1/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func newProject() *v1alpha1.Project {
	return &v1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: "acme"},
		Spec: v1alpha1.ProjectSpec{
			UserVerifierURL: "https://app.acme.com/verify",
			IdentityProvider: v1alpha1.IdentityProviderConfig{
				Issuer:    "https://accounts.google.com",
				Audiences: []string{"mycelium-acme"},
			},
		},
	}
}

func TestProjectReconciler_CreatesNamespace(t *testing.T) {
	scheme := newScheme(t)
	proj := newProject()
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(proj).
		WithStatusSubresource(proj).Build()

	r := &controller.ProjectReconciler{Client: cl, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "acme"},
	})
	require.NoError(t, err)

	var ns corev1.Namespace
	err = cl.Get(context.Background(), types.NamespacedName{Name: "acme"}, &ns)
	require.NoError(t, err)
	assert.Equal(t, "mycelium-controller", ns.Labels["app.kubernetes.io/managed-by"])
}

func TestProjectReconciler_CreatesAGWResources(t *testing.T) {
	scheme := newScheme(t)
	proj := newProject()
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(proj).
		WithStatusSubresource(proj).Build()

	r := &controller.ProjectReconciler{Client: cl, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "acme"},
	})
	require.NoError(t, err)

	// MCP backend
	var backend agwv1alpha1.AgentgatewayBackend
	err = cl.Get(context.Background(), types.NamespacedName{Name: "mycelium-engine", Namespace: "acme"}, &backend)
	require.NoError(t, err)
	require.NotNil(t, backend.Spec.MCP)

	// MCP route
	var route gwv1.HTTPRoute
	err = cl.Get(context.Background(), types.NamespacedName{Name: "mcp-route", Namespace: "acme"}, &route)
	require.NoError(t, err)
	assert.Equal(t, "/mcp", *route.Spec.Rules[0].Matches[0].Path.Value)

	// JWT policy
	var jwtPolicy agwv1alpha1.AgentgatewayPolicy
	err = cl.Get(context.Background(), types.NamespacedName{Name: "jwt-auth", Namespace: "acme"}, &jwtPolicy)
	require.NoError(t, err)
	require.NotNil(t, jwtPolicy.Spec.Traffic.JWTAuthentication)

	// Source context policy
	var srcPolicy agwv1alpha1.AgentgatewayPolicy
	err = cl.Get(context.Background(), types.NamespacedName{Name: "internal-source-context", Namespace: "acme"}, &srcPolicy)
	require.NoError(t, err)
	require.NotNil(t, srcPolicy.Spec.Traffic.Transformation)
}

func TestProjectReconciler_SetsStatusNamespaceRef(t *testing.T) {
	scheme := newScheme(t)
	proj := newProject()
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(proj).
		WithStatusSubresource(proj).Build()

	r := &controller.ProjectReconciler{Client: cl, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "acme"},
	})
	require.NoError(t, err)

	var updated v1alpha1.Project
	err = cl.Get(context.Background(), types.NamespacedName{Name: "acme"}, &updated)
	require.NoError(t, err)
	require.NotNil(t, updated.Status.NamespaceRef)
	assert.Equal(t, "acme", updated.Status.NamespaceRef.Name)
}

func TestProjectReconciler_SetsReadyCondition(t *testing.T) {
	scheme := newScheme(t)
	proj := newProject()
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(proj).
		WithStatusSubresource(proj).Build()

	r := &controller.ProjectReconciler{Client: cl, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "acme"},
	})
	require.NoError(t, err)

	var updated v1alpha1.Project
	err = cl.Get(context.Background(), types.NamespacedName{Name: "acme"}, &updated)
	require.NoError(t, err)

	var ready, nsReady bool
	for _, c := range updated.Status.Conditions {
		if c.Type == "Ready" && c.Status == metav1.ConditionTrue {
			ready = true
		}
		if c.Type == "NamespaceReady" && c.Status == metav1.ConditionTrue {
			nsReady = true
		}
	}
	assert.True(t, ready, "expected Ready condition")
	assert.True(t, nsReady, "expected NamespaceReady condition")
}

func TestProjectReconciler_AddsFinalizer(t *testing.T) {
	scheme := newScheme(t)
	proj := newProject()
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(proj).
		WithStatusSubresource(proj).Build()

	r := &controller.ProjectReconciler{Client: cl, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "acme"},
	})
	require.NoError(t, err)

	var updated v1alpha1.Project
	err = cl.Get(context.Background(), types.NamespacedName{Name: "acme"}, &updated)
	require.NoError(t, err)
	assert.Contains(t, updated.Finalizers, controller.ProjectFinalizer)
}

func TestProjectReconciler_SyncsToolAccessPolicy(t *testing.T) {
	scheme := newScheme(t)
	proj := newProject()

	agent := &v1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: "github-assistant", Namespace: "acme"},
		Spec: v1alpha1.AgentSpec{
			Description: "GitHub agent",
			Tools: []v1alpha1.ToolRef{
				{Ref: corev1.LocalObjectReference{Name: "list-repos"}},
				{Ref: corev1.LocalObjectReference{Name: "create-issue"}},
			},
			Container: v1alpha1.AgentContainer{Image: "acme/gh:latest"},
		},
	}

	tool1 := &v1alpha1.Tool{
		ObjectMeta: metav1.ObjectMeta{Name: "list-repos", Namespace: "acme"},
		Spec: v1alpha1.ToolSpec{
			Container: v1alpha1.ToolContainer{Image: "tools/lr:latest"},
		},
	}
	tool2 := &v1alpha1.Tool{
		ObjectMeta: metav1.ObjectMeta{Name: "create-issue", Namespace: "acme"},
		Spec: v1alpha1.ToolSpec{
			Container: v1alpha1.ToolContainer{Image: "tools/ci:latest"},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(proj, agent, tool1, tool2).
		WithStatusSubresource(proj).Build()

	r := &controller.ProjectReconciler{Client: cl, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "acme"},
	})
	require.NoError(t, err)

	var policy agwv1alpha1.AgentgatewayPolicy
	err = cl.Get(context.Background(), types.NamespacedName{
		Name: "mcp-tool-access", Namespace: "acme",
	}, &policy)
	require.NoError(t, err)
	require.NotNil(t, policy.Spec.Backend)
	require.NotNil(t, policy.Spec.Backend.MCP)
	require.NotNil(t, policy.Spec.Backend.MCP.Authorization)
	assert.Len(t, policy.Spec.Backend.MCP.Authorization.Policy.MatchExpressions, 1)
	assert.Contains(t, string(policy.Spec.Backend.MCP.Authorization.Policy.MatchExpressions[0]), "github-assistant")
}

func TestProjectReconciler_NoAgents_DenyAllPolicy(t *testing.T) {
	scheme := newScheme(t)
	proj := newProject()

	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(proj).
		WithStatusSubresource(proj).Build()

	r := &controller.ProjectReconciler{Client: cl, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "acme"},
	})
	require.NoError(t, err)

	// No agents → deny-all policy
	var policy agwv1alpha1.AgentgatewayPolicy
	err = cl.Get(context.Background(), types.NamespacedName{
		Name: "mcp-tool-access", Namespace: "acme",
	}, &policy)
	require.NoError(t, err)
	require.NotNil(t, policy.Spec.Backend)
	require.NotNil(t, policy.Spec.Backend.MCP)
	require.NotNil(t, policy.Spec.Backend.MCP.Authorization)
	assert.Equal(t, shared.AuthorizationPolicyActionDeny, policy.Spec.Backend.MCP.Authorization.Action)
	assert.Equal(t, shared.CELExpression("true"), policy.Spec.Backend.MCP.Authorization.Policy.MatchExpressions[0])
}

func TestProjectReconciler_DeletionRemovesFinalizer(t *testing.T) {
	scheme := newScheme(t)
	proj := newProject()
	proj.Finalizers = []string{controller.ProjectFinalizer}
	now := metav1.Now()
	proj.DeletionTimestamp = &now

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(proj).
		WithStatusSubresource(proj).Build()

	r := &controller.ProjectReconciler{Client: cl, Scheme: scheme}
	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "acme"},
	})
	require.NoError(t, err)

	// Project should be deleted (fake client removes object when finalizer cleared + DeletionTimestamp set).
	// Namespace cleanup happens via ownerReference garbage collection.
	var updatedProj v1alpha1.Project
	err = cl.Get(context.Background(), types.NamespacedName{Name: "acme"}, &updatedProj)
	assert.True(t, err != nil, "expected project to be deleted after finalizer removal")
}

func TestProjectReconciler_DeletionRequeuesWithDependents(t *testing.T) {
	scheme := newScheme(t)
	proj := newProject()
	proj.Finalizers = []string{controller.ProjectFinalizer}
	now := metav1.Now()
	proj.DeletionTimestamp = &now

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "acme"},
	}

	tool := &v1alpha1.Tool{
		ObjectMeta: metav1.ObjectMeta{Name: "list-repos", Namespace: "acme"},
		Spec: v1alpha1.ToolSpec{
			Description: "d",
			Container:   v1alpha1.ToolContainer{Image: "i"},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(proj, ns, tool).
		WithStatusSubresource(proj).Build()

	r := &controller.ProjectReconciler{Client: cl, Scheme: scheme}
	result, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "acme"},
	})
	require.NoError(t, err)
	assert.NotZero(t, result.RequeueAfter, "expected requeue when dependents exist")

	// Namespace should still exist
	var updatedNS corev1.Namespace
	err = cl.Get(context.Background(), types.NamespacedName{Name: "acme"}, &updatedNS)
	require.NoError(t, err)

	// Project should still have its finalizer
	var updatedProj v1alpha1.Project
	err = cl.Get(context.Background(), types.NamespacedName{Name: "acme"}, &updatedProj)
	require.NoError(t, err)
	assert.Contains(t, updatedProj.Finalizers, controller.ProjectFinalizer)
}

func TestProjectReconciler_NotFound(t *testing.T) {
	scheme := newScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	r := &controller.ProjectReconciler{Client: cl, Scheme: scheme}
	result, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "gone"},
	})
	require.NoError(t, err)
	assert.False(t, result.Requeue)
}
