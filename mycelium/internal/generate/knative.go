package generate

import (
	"fmt"

	v1alpha1 "github.com/mongodb/mycelium/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	knservingv1 "knative.dev/serving/pkg/apis/serving/v1"
)

// KnativeService generates a Knative Service from a Tool.
func KnativeService(tool *v1alpha1.Tool) *knservingv1.Service {
	minScale := int32(0)
	maxScale := int32(10)
	if tool.Spec.Scaling != nil {
		if tool.Spec.Scaling.MinScale != nil {
			minScale = *tool.Spec.Scaling.MinScale
		}
		if tool.Spec.Scaling.MaxScale != nil {
			maxScale = *tool.Spec.Scaling.MaxScale
		}
	}

	labels := managedLabels()
	labels["mycelium.io/tool"] = tool.Name

	return &knservingv1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("tool-%s", tool.Name),
			Namespace: tool.Namespace,
			Labels:    labels,
		},
		Spec: knservingv1.ServiceSpec{
			ConfigurationSpec: knservingv1.ConfigurationSpec{
				Template: knservingv1.RevisionTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							"autoscaling.knative.dev/minScale": fmt.Sprintf("%d", minScale),
							"autoscaling.knative.dev/maxScale": fmt.Sprintf("%d", maxScale),
						},
					},
					Spec: knservingv1.RevisionSpec{
						ContainerConcurrency: ptr.To[int64](1),
						PodSpec: corev1.PodSpec{
							RuntimeClassName: ptr.To("kata-fc"),
							Containers: []corev1.Container{{
								Image: tool.Spec.Container.Image,
								Ports: []corev1.ContainerPort{{
									ContainerPort: 8080,
								}},
							}},
						},
					},
				},
			},
		},
	}
}

func managedLabels() map[string]string {
	return map[string]string{"mycelium.io/managed-by": "controller"}
}
