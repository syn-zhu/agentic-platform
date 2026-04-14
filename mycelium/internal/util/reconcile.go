package util

import (
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
)

// LowestNonZeroResult returns the result with the lowest non-zero RequeueAfter,
// preferring Requeue over no-requeue. Used to merge results from multiple
// reconciliation phases.
func LowestNonZeroResult(a, b ctrl.Result) ctrl.Result {
	switch {
	case a.IsZero():
		return b
	case b.IsZero():
		return a
	case a.Requeue:
		return a
	case b.Requeue:
		return b
	default:
		return ctrl.Result{RequeueAfter: minDuration(a.RequeueAfter, b.RequeueAfter)}
	}
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
