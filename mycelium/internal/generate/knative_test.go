package generate_test

import (
	"testing"

	v1alpha1 "github.com/mongodb/mycelium/api/v1alpha1"
	"github.com/mongodb/mycelium/internal/generate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestKnativeService_Defaults(t *testing.T) {
	tool := &v1alpha1.Tool{
		ObjectMeta: metav1.ObjectMeta{Name: "list-repos", Namespace: "tenant-a"},
		Spec: v1alpha1.ToolSpec{
			Container: v1alpha1.ToolContainer{Image: "tenant-a/tool-list-repos:latest"},
		},
	}

	svc := generate.KnativeService(tool)
	assert.Equal(t, "list-repos", svc.Name)
	assert.Equal(t, "tenant-a", svc.Namespace)
	assert.Equal(t, "mycelium-controller", svc.Labels["app.kubernetes.io/managed-by"])
	assert.Equal(t, "list-repos", svc.Annotations["mycelium.io/tool"])

	// containerConcurrency = 1
	require.NotNil(t, svc.Spec.Template.Spec.ContainerConcurrency)
	assert.Equal(t, int64(1), *svc.Spec.Template.Spec.ContainerConcurrency)

	// runtimeClassName = kata-fc
	require.NotNil(t, svc.Spec.Template.Spec.RuntimeClassName)
	assert.Equal(t, "kata-fc", *svc.Spec.Template.Spec.RuntimeClassName)

	// No scaling specified → no scaling annotations
	assert.Empty(t, svc.Spec.Template.Annotations["autoscaling.knative.dev/minScale"])
	assert.Empty(t, svc.Spec.Template.Annotations["autoscaling.knative.dev/maxScale"])

	// Container
	require.Len(t, svc.Spec.Template.Spec.Containers, 1)
	assert.Equal(t, "tenant-a/tool-list-repos:latest", svc.Spec.Template.Spec.Containers[0].Image)
	assert.Equal(t, int32(8080), svc.Spec.Template.Spec.Containers[0].Ports[0].ContainerPort)
}

func TestKnativeService_CustomScaling(t *testing.T) {
	tool := &v1alpha1.Tool{
		ObjectMeta: metav1.ObjectMeta{Name: "heavy-tool", Namespace: "tenant-a"},
		Spec: v1alpha1.ToolSpec{
			Container: v1alpha1.ToolContainer{Image: "tools/heavy:latest"},
			Scaling: &v1alpha1.ToolScaling{
				MinScale: ptr.To[int32](2),
				MaxScale: ptr.To[int32](50),
			},
		},
	}

	svc := generate.KnativeService(tool)
	assert.Equal(t, "2", svc.Spec.Template.Annotations["autoscaling.knative.dev/minScale"])
	assert.Equal(t, "50", svc.Spec.Template.Annotations["autoscaling.knative.dev/maxScale"])
}

func TestKnativeService_NilScaling(t *testing.T) {
	tool := &v1alpha1.Tool{
		ObjectMeta: metav1.ObjectMeta{Name: "simple", Namespace: "tenant-b"},
		Spec: v1alpha1.ToolSpec{
			Container: v1alpha1.ToolContainer{Image: "tools/simple:v1"},
		},
	}

	svc := generate.KnativeService(tool)
	// No scaling specified → no scaling annotations
	assert.Empty(t, svc.Spec.Template.Annotations["autoscaling.knative.dev/minScale"])
	assert.Empty(t, svc.Spec.Template.Annotations["autoscaling.knative.dev/maxScale"])
}
