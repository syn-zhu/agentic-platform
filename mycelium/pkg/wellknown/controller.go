package wellknown

const (
	// DefaultMyceliumControllerName is the name of the mycelium controller
	// TODO(mycelium): Make this configurable — look at how AgentGateway does it
	DefaultMyceliumControllerName = "mycelium.io/mycelium"

	// LeaderElectionID is the name of the lease that leader election will use for holding the leader lock.
	LeaderElectionID = "mycelium-leader"
)
