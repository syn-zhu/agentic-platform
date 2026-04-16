package v1alpha1

// Condition type constants.
const (
	ReadyCondition = "Ready"

	// Per-resource condition types reported on Project status.
	NamespaceCondition           = "Namespace"
	MCPBackendCondition          = "MCPBackend"
	MCPRouteCondition            = "MCPRoute"
	JWTPolicyCondition           = "JWTPolicy"
	SourceContextPolicyCondition = "SourceContextPolicy"
	ToolAccessPolicyCondition    = "ToolAccessPolicy"

	// Per-resource condition types reported on Agent and Tool status.
	ServiceAccountCondition = "ServiceAccount"
	KnativeServiceCondition = "KnativeService"
)

// Reason constants.
const (
	PendingReason      = "Pending"
	ProvisioningReason = "Provisioning"
	ProgressingReason  = "Progressing"
	RunningReason      = "Running"
	FailedReason       = "Failed"
	InvalidReason      = "Invalid"
	TerminatingReason  = "Terminating"

	// Legacy reasons kept for other reconcilers.
	SucceededReason = "Succeeded"
	CreatedReason   = "Created"
	SyncedReason    = "Synced"

	ProjectNotFoundReason = "ProjectNotFound"
	ProjectDeletingReason = "ProjectDeleting"
	SecretNotFoundReason  = "SecretNotFound"
	SecretDeletingReason  = "SecretDeleting"
)
