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
// Scaling annotations are only set when explicitly specified by the user.
// When omitted, Knative's own defaults apply.
func KnativeService(tool *v1alpha1.Tool) *knservingv1.Service {
	labels := ManagedLabels()
	annotations := ToolAnnotations(tool.Name)
	if tool.Spec.Scaling != nil {
		if tool.Spec.Scaling.MinScale != nil {
			annotations["autoscaling.knative.dev/minScale"] = fmt.Sprintf("%d", *tool.Spec.Scaling.MinScale)
		}
		if tool.Spec.Scaling.MaxScale != nil {
			annotations["autoscaling.knative.dev/maxScale"] = fmt.Sprintf("%d", *tool.Spec.Scaling.MaxScale)
		}
	}

	return &knservingv1.Service{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "serving.knative.dev/v1",
			Kind:       "Service",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        tool.Name,
			Namespace:   tool.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: knservingv1.ServiceSpec{
			ConfigurationSpec: knservingv1.ConfigurationSpec{
				Template: knservingv1.RevisionTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: annotations,
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

