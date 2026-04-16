package generate

import (
	"fmt"

	v1alpha1 "mycelium.io/mycelium/api/v1alpha1"
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
	for k, v := range ToolLabels(tool.Name) {
		labels[k] = v
	}
	annotations := map[string]string{}
	if tool.Spec.WorkerPool.MinReplicas != nil {
		annotations["autoscaling.knative.dev/minScale"] = fmt.Sprintf("%d", *tool.Spec.WorkerPool.MinReplicas)
	}
	if tool.Spec.WorkerPool.MaxReplicas != nil {
		annotations["autoscaling.knative.dev/maxScale"] = fmt.Sprintf("%d", *tool.Spec.WorkerPool.MaxReplicas)
	}

	return &knservingv1.Service{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "serving.knative.dev/v1",
			Kind:       "Service",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      tool.Name,
			Namespace: tool.Namespace,
			Labels:    labels,
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
								Image: tool.Spec.WorkerPool.Image,
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
