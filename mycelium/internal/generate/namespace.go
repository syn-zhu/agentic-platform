package generate

import (
	v1alpha1 "mycelium.io/mycelium/api/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Namespace generates the Namespace owned by a Project.
func Namespace(p *v1alpha1.MyceliumEcosystem) *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   p.Name,
			Labels: ProjectLabels(p.Name),
		},
	}
}
