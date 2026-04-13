package v1alpha1_test

import (
	"testing"

	"github.com/mongodb/mycelium/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAgent_HasExpectedFields(t *testing.T) {
	agent := &v1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "github-assistant",
			Namespace: "acme",
		},
		Spec: v1alpha1.AgentSpec{
			Description: "GitHub integration agent",
			Tools: []v1alpha1.ToolRef{
				{Ref: corev1.LocalObjectReference{Name: "list-repos"}},
				{Ref: corev1.LocalObjectReference{Name: "create-issue"}},
			},
			Container: v1alpha1.AgentContainer{
				Image: "acme/github-assistant:latest",
			},
			Sandbox: &v1alpha1.SandboxConfig{
				ShutdownTimeout: "30m",
				WarmPool: &v1alpha1.WarmPoolConfig{
					Replicas: 2,
				},
			},
		},
	}

	assert.Equal(t, "GitHub integration agent", agent.Spec.Description)
	require.Len(t, agent.Spec.Tools, 2)
	assert.Equal(t, "list-repos", agent.Spec.Tools[0].Ref.Name)
	assert.Equal(t, "create-issue", agent.Spec.Tools[1].Ref.Name)
	assert.Equal(t, "acme/github-assistant:latest", agent.Spec.Container.Image)
	require.NotNil(t, agent.Spec.Sandbox)
	assert.Equal(t, "30m", agent.Spec.Sandbox.ShutdownTimeout)
	assert.Equal(t, int32(2), agent.Spec.Sandbox.WarmPool.Replicas)
}

func TestAgent_ToolsRequired(t *testing.T) {
	agent := &v1alpha1.Agent{
		Spec: v1alpha1.AgentSpec{
			Description: "Minimal agent",
			Tools: []v1alpha1.ToolRef{
				{Ref: corev1.LocalObjectReference{Name: "echo"}},
			},
			Container: v1alpha1.AgentContainer{Image: "tools/echo:latest"},
		},
	}
	require.Len(t, agent.Spec.Tools, 1)
	assert.Nil(t, agent.Spec.Sandbox)
}

func TestAgent_StatusConditions(t *testing.T) {
	agent := &v1alpha1.Agent{
		Status: v1alpha1.AgentStatus{
			ServiceAccountRef: &corev1.LocalObjectReference{Name: "github-assistant"},
			Conditions: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Reconciled"},
				{Type: "ToolsValid", Status: metav1.ConditionTrue, Reason: "AllToolsExist"},
			},
		},
	}
	assert.Equal(t, "github-assistant", agent.Status.ServiceAccountRef.Name)
	assert.Len(t, agent.Status.Conditions, 2)
}
