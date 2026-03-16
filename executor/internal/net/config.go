package net

const (
	// Default TAP device name. Must match the pod's
	// istio.io/reroute-virtual-interfaces annotation.
	DefaultTAPName = "fctr-tap0"

	// Link-local addresses for the point-to-point TAP link.
	DefaultHostIP  = "169.254.1.1"
	DefaultGuestIP = "169.254.1.2"
)

// Config holds routed TAP networking configuration.
type Config struct {
	TAPName string
	HostIP  string
	GuestIP string
	MTU     int
}

// GuestNetConfig is the network configuration passed to the guest via vsock Init.
type GuestNetConfig struct {
	IP        string
	Gateway   string
	PrefixLen int
	MTU       int
}

// GuestNetworkConfig returns the configuration the guest needs to set up eth0.
func (c *Config) GuestNetworkConfig() GuestNetConfig {
	return GuestNetConfig{
		IP:        c.GuestIP,
		Gateway:   c.HostIP,
		PrefixLen: 32,
		MTU:       c.MTU,
	}
}
