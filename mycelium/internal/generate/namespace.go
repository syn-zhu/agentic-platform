package generate

import (
	v1alpha1 "mycelium.io/mycelium/api/v1alpha1"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
)

// Namespace generates the apply configuration for the namespace owned by a Project.
func Namespace(p *v1alpha1.Project) *corev1ac.NamespaceApplyConfiguration {
	return corev1ac.Namespace(p.Name).
		WithLabels(ManagedLabels()).
		WithLabels(ProjectLabels(p.Name))
}
