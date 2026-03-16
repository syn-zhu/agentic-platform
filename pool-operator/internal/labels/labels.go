// pool-operator/internal/labels/labels.go
package labels

const (
	// LabelPool identifies which ExecutorPool a pod belongs to.
	LabelPool = "agentic.example.com/pool"

	// LabelStatus tracks the pod's lifecycle state: warming, available, claimed.
	LabelStatus = "agentic.example.com/status"

	// LabelClaimID stores the claim ID when a pod is claimed.
	LabelClaimID = "agentic.example.com/claim-id"

	// AnnotationLeaseExpiresAt stores the RFC3339 lease expiry timestamp.
	AnnotationLeaseExpiresAt = "agentic.example.com/lease-expires-at"

	// Status values
	StatusWarming   = "warming"
	StatusAvailable = "available"
	StatusClaimed   = "claimed"
)
