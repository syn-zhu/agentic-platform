package controller

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// phaseStatus is the outcome of a single reconcile phase.
type phaseStatus int

const (
	// phaseDone means the sub-resource was applied and the owning controller
	// has confirmed it is fully ready (e.g., Accepted=True).
	phaseDone phaseStatus = iota

	// phaseProgressing means the sub-resource was applied but is not yet ready.
	// No explicit requeue is needed — the Owns() watch fires when the child
	// controller updates the resource's status.
	phaseProgressing

	// phaseFailed means the sub-resource hit a terminal error (schema invalid,
	// admission rejected, etc.). The per-resource condition has already been set;
	// no requeue will help without an external fix.
	phaseFailed
)

// findCondition returns the first condition with the given type, or nil.
func findCondition(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}
