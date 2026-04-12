package v1alpha1_test

import (
	"testing"

	"github.com/mongodb/mycelium/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestToolConfig_HasExpectedFields(t *testing.T) {
	tc := &v1alpha1.ToolConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "list-repos",
			Namespace: "tenant-a",
		},
		Spec: v1alpha1.ToolConfigSpec{
			ToolName:    "list_repos",
			Description: "List GitHub repos for an org.",
			Resource: &v1alpha1.ResourceBinding{
				Ref: v1alpha1.ResourceRef{
					Group: "mycelium.io",
					Kind:  "OAuthResource",
					Name:  "github",
				},
				Scopes: []string{"repo"},
			},
			InputSchema: &apiextv1.JSON{Raw: []byte(`{"type":"object","properties":{"org":{"type":"string"}}}`)},
			Container: v1alpha1.ToolContainer{
				Image: "tenant-a/tool-list-repos:latest",
			},
		},
	}

	assert.Equal(t, "list_repos", tc.Spec.ToolName)
	assert.Equal(t, "List GitHub repos for an org.", tc.Spec.Description)
	assert.Equal(t, "mycelium.io", tc.Spec.Resource.Ref.Group)
	assert.Equal(t, "OAuthResource", tc.Spec.Resource.Ref.Kind)
	assert.Equal(t, "github", tc.Spec.Resource.Ref.Name)
	assert.Equal(t, []string{"repo"}, tc.Spec.Resource.Scopes)
	assert.Equal(t, "tenant-a/tool-list-repos:latest", tc.Spec.Container.Image)
}

func TestToolConfig_ResourceIsOptional(t *testing.T) {
	tc := &v1alpha1.ToolConfig{
		Spec: v1alpha1.ToolConfigSpec{
			ToolName:    "echo",
			Description: "Echoes input.",
			Container: v1alpha1.ToolContainer{
				Image: "tools/echo:latest",
			},
		},
	}
	assert.Nil(t, tc.Spec.Resource)
}

func TestToolConfig_ScalingDefaults(t *testing.T) {
	tc := &v1alpha1.ToolConfig{
		Spec: v1alpha1.ToolConfigSpec{
			ToolName:  "test",
			Container: v1alpha1.ToolContainer{Image: "test:latest"},
		},
	}
	assert.Nil(t, tc.Spec.Scaling)

	tc2 := &v1alpha1.ToolConfig{
		Spec: v1alpha1.ToolConfigSpec{
			ToolName:  "test",
			Container: v1alpha1.ToolContainer{Image: "test:latest"},
			Scaling: &v1alpha1.ToolScaling{
				MinScale: ptrInt32(2),
				MaxScale: ptrInt32(50),
			},
		},
	}
	assert.Equal(t, int32(2), *tc2.Spec.Scaling.MinScale)
	assert.Equal(t, int32(50), *tc2.Spec.Scaling.MaxScale)
}

func ptrInt32(v int32) *int32 { return &v }
