package v1alpha1_test

import (
	"testing"

	"github.com/mongodb/mycelium/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestTool_WithOAuthAndAPIKeys(t *testing.T) {
	tool := &v1alpha1.Tool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "list-repos",
			Namespace: "tenant-a",
		},
		Spec: v1alpha1.ToolSpec{
			ToolName:    "list_repos",
			Description: "List GitHub repos for an org.",
			Credentials: &v1alpha1.ToolCredentials{
				OAuth: &v1alpha1.OAuthCredentialRef{
					ProviderRef: corev1.LocalObjectReference{Name: "github"},
					Scopes:      []string{"repo"},
				},
				APIKeys: []v1alpha1.APIKeyCredentialRef{
					{ProviderRef: corev1.LocalObjectReference{Name: "stripe-api"}},
					{ProviderRef: corev1.LocalObjectReference{Name: "sendgrid"}},
				},
			},
			InputSchema: &apiextv1.JSON{Raw: []byte(`{"type":"object","properties":{"org":{"type":"string"}}}`)},
			Container: v1alpha1.ToolContainer{
				Image: "tenant-a/tool-list-repos:latest",
			},
		},
	}

	assert.Equal(t, "list_repos", tool.Spec.ToolName)
	assert.Equal(t, "github", tool.Spec.Credentials.OAuth.ProviderRef.Name)
	assert.Equal(t, []string{"repo"}, tool.Spec.Credentials.OAuth.Scopes)
	assert.Len(t, tool.Spec.Credentials.APIKeys, 2)
	assert.Equal(t, "stripe-api", tool.Spec.Credentials.APIKeys[0].ProviderRef.Name)
	assert.Equal(t, "sendgrid", tool.Spec.Credentials.APIKeys[1].ProviderRef.Name)
}

func TestTool_OAuthOnly(t *testing.T) {
	tool := &v1alpha1.Tool{
		Spec: v1alpha1.ToolSpec{
			ToolName:    "create_issue",
			Description: "Create a GitHub issue.",
			Credentials: &v1alpha1.ToolCredentials{
				OAuth: &v1alpha1.OAuthCredentialRef{
					ProviderRef: corev1.LocalObjectReference{Name: "github"},
					Scopes:      []string{"repo", "issues"},
				},
			},
			Container: v1alpha1.ToolContainer{Image: "tools/create-issue:latest"},
		},
	}

	assert.NotNil(t, tool.Spec.Credentials.OAuth)
	assert.Nil(t, tool.Spec.Credentials.APIKeys)
}

func TestTool_NoCredentials(t *testing.T) {
	tool := &v1alpha1.Tool{
		Spec: v1alpha1.ToolSpec{
			ToolName:    "echo",
			Description: "Echoes input.",
			Container:   v1alpha1.ToolContainer{Image: "tools/echo:latest"},
		},
	}
	assert.Nil(t, tool.Spec.Credentials)
}

func TestTool_ScalingOverrides(t *testing.T) {
	tool := &v1alpha1.Tool{
		Spec: v1alpha1.ToolSpec{
			ToolName:  "test",
			Container: v1alpha1.ToolContainer{Image: "test:latest"},
			Scaling: &v1alpha1.ToolScaling{
				MinScale: ptrInt32(2),
				MaxScale: ptrInt32(50),
			},
		},
	}
	assert.Equal(t, int32(2), *tool.Spec.Scaling.MinScale)
	assert.Equal(t, int32(50), *tool.Spec.Scaling.MaxScale)
}

func TestTool_StatusKnativeServiceRef(t *testing.T) {
	tool := &v1alpha1.Tool{
		Status: v1alpha1.ToolStatus{
			KnativeServiceRef: &corev1.LocalObjectReference{
				Name: "tool-list-repos",
			},
			Conditions: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Reconciled"},
				{Type: "KnativeServiceReady", Status: metav1.ConditionTrue, Reason: "ServiceAvailable"},
				{Type: "CredentialsValid", Status: metav1.ConditionTrue, Reason: "ProvidersResolved"},
			},
		},
	}

	assert.Equal(t, "tool-list-repos", tool.Status.KnativeServiceRef.Name)
	assert.Len(t, tool.Status.Conditions, 3)
	assert.Equal(t, "CredentialsValid", tool.Status.Conditions[2].Type)
}

func ptrInt32(v int32) *int32 { return &v }
