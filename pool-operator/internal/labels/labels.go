// pool-operator/internal/labels/labels.go
package labels

const (
	// LabelPool identifies which ExecutorPool a pod belongs to.
	LabelPool = "pool.agentic.dev/pool"

	// LabelStatus tracks the pod's lifecycle state: warming, available, claimed.
	LabelStatus = "pool.agentic.dev/status"

	// LabelClaimID stores the claim ID when a pod is claimed.
	LabelClaimID = "pool.agentic.dev/claim-id"

	// AnnotationLeaseExpiresAt stores the RFC3339 lease expiry timestamp.
	AnnotationLeaseExpiresAt = "pool.agentic.dev/lease-expires-at"

	// Status values
	StatusWarming   = "warming"
	StatusAvailable = "available"
	StatusClaimed   = "claimed"
)
