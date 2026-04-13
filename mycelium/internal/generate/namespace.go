package generate

import (
	v1alpha1 "github.com/mongodb/mycelium/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Namespace generates the namespace owned by a Project.
func Namespace(p *v1alpha1.Project) *corev1.Namespace {
	return &corev1.Namespace{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Namespace",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        p.Name,
			Labels:      ManagedLabels(),
			Annotations: ProjectAnnotations(p.Name),
		},
	}
}
