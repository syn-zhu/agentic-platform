package generate

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1alpha1 "mycelium.io/mycelium/api/v1alpha1"
)

// ServiceAccount generates a per-agent ServiceAccount. The SA name matches
// the agent name and is used for identity resolution in tool-access policy
// CEL expressions (source.workload.unverified.serviceAccount).
func ServiceAccount(agent *v1alpha1.MyceliumAgent) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:        agent.Name,
			Namespace:   agent.Namespace,
			Annotations: AgentLabels(agent.Name),
		},
	}
}
