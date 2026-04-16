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

// --- DELETE ---

func TestProjectValidator_DeleteAllowsWhenEmpty(t *testing.T) {
	scheme := newScheme(t)
	proj := &v1alpha1.Project{ObjectMeta: metav1.ObjectMeta{Name: "acme"}}
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	v := &webhook.ProjectValidator{Client: cl}
	_, err := v.ValidateDelete(context.Background(), proj)
	assert.NoError(t, err)
}

func TestProjectValidator_DeleteRejectsWithTools(t *testing.T) {
	scheme := newScheme(t)
	proj := &v1alpha1.Project{ObjectMeta: metav1.ObjectMeta{Name: "acme"}}
	tool := &v1alpha1.Tool{
		ObjectMeta: metav1.ObjectMeta{
			Name: "list-repos", Namespace: "acme",
		},
		Spec: v1alpha1.ToolSpec{
			Description: "d",
			Container:   v1alpha1.ToolContainer{Image: "i"},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tool).Build()

	v := &webhook.ProjectValidator{Client: cl}
	_, err := v.ValidateDelete(context.Background(), proj)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Tool")
}

func TestProjectValidator_DeleteRejectsWithCredentialProviders(t *testing.T) {
	scheme := newScheme(t)
	proj := &v1alpha1.Project{ObjectMeta: metav1.ObjectMeta{Name: "acme"}}
	cp := &v1alpha1.CredentialProvider{
		ObjectMeta: metav1.ObjectMeta{
			Name: "github", Namespace: "acme",
		},
		Spec: v1alpha1.CredentialProviderSpec{
			APIKey: &v1alpha1.APIKeyProviderSpec{
				APIKeySecretRef: corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "s"},
					Key:                  "k",
				},
			},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cp).Build()

	v := &webhook.ProjectValidator{Client: cl}
	_, err := v.ValidateDelete(context.Background(), proj)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CredentialProvider")
}

func TestProjectValidator_DeleteRejectsWithAgents(t *testing.T) {
	scheme := newScheme(t)
	proj := &v1alpha1.Project{ObjectMeta: metav1.ObjectMeta{Name: "acme"}}
	agent := &v1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gh", Namespace: "acme",
		},
		Spec: v1alpha1.AgentSpec{
			Description: "d",
			Tools:       []v1alpha1.ToolRef{{Ref: corev1.LocalObjectReference{Name: "t"}}},
			Container:   v1alpha1.AgentContainer{Image: "i"},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(agent).Build()

	v := &webhook.ProjectValidator{Client: cl}
	_, err := v.ValidateDelete(context.Background(), proj)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Agent")
}
