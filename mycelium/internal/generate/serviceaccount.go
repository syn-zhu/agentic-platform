package generate

import (
	v1alpha1 "mycelium.io/mycelium/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ServiceAccount generates a per-agent ServiceAccount. The SA name matches
// the agent name and is used for identity resolution in tool-access policy
// CEL expressions (source.workload.unverified.serviceAccount).
func ServiceAccount(agent *v1alpha1.Agent) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ServiceAccount",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        agent.Name,
			Namespace:   agent.Namespace,
			Labels:      ManagedLabels(),
			Annotations: AgentLabels(agent.Name),
		},
	}
}
