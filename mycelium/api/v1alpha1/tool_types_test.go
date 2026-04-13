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
			Description: "List GitHub repos for an org.",
			Credentials: []v1alpha1.CredentialBinding{
				{
					OAuth: &v1alpha1.OAuthCredentialBinding{
						ProviderRef: corev1.LocalObjectReference{Name: "github"},
						Scopes:      []string{"repo"},
					},
				},
				{
					APIKey: &v1alpha1.APIKeyCredentialBinding{
						ProviderRef: corev1.LocalObjectReference{Name: "stripe-api"},
					},
				},
				{
					APIKey: &v1alpha1.APIKeyCredentialBinding{
						ProviderRef: corev1.LocalObjectReference{Name: "sendgrid"},
					},
				},
			},
			InputSchema: &apiextv1.JSON{Raw: []byte(`{"type":"object","properties":{"org":{"type":"string"}}}`)},
			Container: v1alpha1.ToolContainer{
				Image: "tenant-a/tool-list-repos:latest",
			},
		},
	}

	assert.Equal(t, "list-repos", tool.Name)
	assert.Len(t, tool.Spec.Credentials, 3)
	assert.True(t, tool.Spec.Credentials[0].IsOAuth())
	assert.Equal(t, "github", tool.Spec.Credentials[0].ProviderName())
	assert.Equal(t, []string{"repo"}, tool.Spec.Credentials[0].OAuth.Scopes)
	assert.True(t, tool.Spec.Credentials[1].IsAPIKey())
	assert.Equal(t, "stripe-api", tool.Spec.Credentials[1].ProviderName())
	assert.Equal(t, "sendgrid", tool.Spec.Credentials[2].ProviderName())
}

func TestTool_OAuthOnly(t *testing.T) {
	tool := &v1alpha1.Tool{
		Spec: v1alpha1.ToolSpec{
			Description: "Create a GitHub issue.",
			Credentials: []v1alpha1.CredentialBinding{
				{
					OAuth: &v1alpha1.OAuthCredentialBinding{
						ProviderRef: corev1.LocalObjectReference{Name: "github"},
						Scopes:      []string{"repo", "issues"},
					},
				},
			},
			Container: v1alpha1.ToolContainer{Image: "tools/create-issue:latest"},
		},
	}

	assert.Len(t, tool.Spec.Credentials, 1)
	assert.True(t, tool.Spec.Credentials[0].IsOAuth())
}

func TestTool_NoCredentials(t *testing.T) {
	tool := &v1alpha1.Tool{
		Spec: v1alpha1.ToolSpec{
			Description: "Echoes input.",
			Container:   v1alpha1.ToolContainer{Image: "tools/echo:latest"},
		},
	}
	assert.Empty(t, tool.Spec.Credentials)
}

func TestTool_ScalingOverrides(t *testing.T) {
	tool := &v1alpha1.Tool{
		Spec: v1alpha1.ToolSpec{
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

func TestTool_StatusServiceRef(t *testing.T) {
	tool := &v1alpha1.Tool{
		Status: v1alpha1.ToolStatus{
			ServiceRef: &corev1.LocalObjectReference{
				Name: "list-repos",
			},
			Conditions: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue, Reason: "Reconciled"},
				{Type: "ServiceReady", Status: metav1.ConditionTrue, Reason: "ServiceAvailable"},
				{Type: "CredentialsValid", Status: metav1.ConditionTrue, Reason: "ProvidersResolved"},
			},
		},
	}

	assert.Equal(t, "list-repos", tool.Status.ServiceRef.Name)
	assert.Len(t, tool.Status.Conditions, 3)
	assert.Equal(t, "CredentialsValid", tool.Status.Conditions[2].Type)
}

func TestCredentialRef_Helpers(t *testing.T) {
	oauth := v1alpha1.CredentialBinding{
		OAuth: &v1alpha1.OAuthCredentialBinding{
			ProviderRef: corev1.LocalObjectReference{Name: "github"},
			Scopes:      []string{"repo"},
		},
	}
	assert.True(t, oauth.IsOAuth())
	assert.False(t, oauth.IsAPIKey())
	assert.Equal(t, "github", oauth.ProviderName())

	apiKey := v1alpha1.CredentialBinding{
		APIKey: &v1alpha1.APIKeyCredentialBinding{
			ProviderRef: corev1.LocalObjectReference{Name: "stripe"},
		},
	}
	assert.False(t, apiKey.IsOAuth())
	assert.True(t, apiKey.IsAPIKey())
	assert.Equal(t, "stripe", apiKey.ProviderName())
}

func ptrInt32(v int32) *int32 { return &v }
