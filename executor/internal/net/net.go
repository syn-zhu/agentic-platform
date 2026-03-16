//go:build linux

package net

import (
	"fmt"
	"log/slog"
	"net"
	"os/exec"

	"github.com/vishvananda/netlink"
)

const (
	// ztunnel DNS proxy port (bound to 127.0.0.1 inside the pod netns).
	ztunnelDNSPort = 15053
)

// DefaultConfig returns the standard routed TAP configuration
// with MTU discovered from the host's default-route interface.
func DefaultConfig() *Config {
	mtu, err := DiscoverMTU()
	if err != nil {
		mtu = 1500
	}
	return &Config{
		TAPName: DefaultTAPName,
		HostIP:  DefaultHostIP,
		GuestIP: DefaultGuestIP,
		MTU:     mtu,
	}
}

// DiscoverMTU reads the MTU from the default-route interface.
func DiscoverMTU() (int, error) {
	routes, err := netlink.RouteList(nil, netlink.FAMILY_V4)
	if err != nil {
		return 0, fmt.Errorf("list routes: %w", err)
	}
	for _, r := range routes {
		if r.Dst == nil { // default route
			link, err := netlink.LinkByIndex(r.LinkIndex)
			if err != nil {
				return 0, fmt.Errorf("lookup link %d: %w", r.LinkIndex, err)
			}
			return link.Attrs().MTU, nil
		}
	}
	return 1500, nil
}

// Setup creates the TAP device, assigns host-side IP, adds the guest route,
// and installs the DNS DNAT rule for ztunnel interception.
func Setup(cfg *Config) error {
	slog.Info("setting up routed TAP",
		"tap", cfg.TAPName, "host_ip", cfg.HostIP, "guest_ip", cfg.GuestIP, "mtu", cfg.MTU)

	// 1. Create TAP device.
	tap := &netlink.Tuntap{
		LinkAttrs: netlink.LinkAttrs{
			Name: cfg.TAPName,
			MTU:  cfg.MTU,
		},
		Mode:  netlink.TUNTAP_MODE_TAP,
		Flags: netlink.TUNTAP_VNET_HDR | netlink.TUNTAP_NO_PI,
	}

	if err := netlink.LinkAdd(tap); err != nil {
		return fmt.Errorf("create TAP %s: %w", cfg.TAPName, err)
	}

	link, err := netlink.LinkByName(cfg.TAPName)
	if err != nil {
		return fmt.Errorf("lookup TAP: %w", err)
	}

	// 2. Assign host-side IP.
	hostAddr, err := netlink.ParseAddr(cfg.HostIP + "/32")
	if err != nil {
		return fmt.Errorf("parse host addr: %w", err)
	}
	if err := netlink.AddrAdd(link, hostAddr); err != nil {
		return fmt.Errorf("add host addr to TAP: %w", err)
	}

	// 3. Bring TAP up.
	if err := netlink.LinkSetUp(link); err != nil {
		return fmt.Errorf("bring TAP up: %w", err)
	}

	// 4. Add route to guest via TAP.
	guestIP := net.ParseIP(cfg.GuestIP)
	if guestIP == nil {
		return fmt.Errorf("invalid guest IP: %s", cfg.GuestIP)
	}
	route := &netlink.Route{
		Dst:       &net.IPNet{IP: guestIP, Mask: net.CIDRMask(32, 32)},
		LinkIndex: link.Attrs().Index,
		Scope:     netlink.SCOPE_LINK,
	}
	if err := netlink.RouteAdd(route); err != nil {
		return fmt.Errorf("add guest route: %w", err)
	}

	// 5. Add DNS DNAT rule.
	// ztunnel's DNS proxy listens on 127.0.0.1:15053 (localhost-bound).
	// The istio.io/reroute-virtual-interfaces annotation only adds a TCP
	// REDIRECT rule. We need to DNAT UDP port 53 to ztunnel's DNS proxy.
	// See https://github.com/istio/istio/issues/54020
	if err := addDNSRule(cfg.TAPName); err != nil {
		return fmt.Errorf("add DNS DNAT rule: %w", err)
	}

	slog.Info("routed TAP setup complete", "tap", cfg.TAPName)
	return nil
}

// Teardown removes the DNS DNAT rule, guest route, and TAP device.
func Teardown(cfg *Config) error {
	slog.Info("tearing down routed TAP", "tap", cfg.TAPName)

	// 1. Remove DNS DNAT rule (best-effort).
	if err := removeDNSRule(cfg.TAPName); err != nil {
		slog.Warn("failed to remove DNS DNAT rule", "error", err)
	}

	// 2. Remove guest route and delete TAP.
	link, err := netlink.LinkByName(cfg.TAPName)
	if err != nil {
		slog.Warn("TAP not found during teardown", "tap", cfg.TAPName, "error", err)
		return nil
	}

	guestIP := net.ParseIP(cfg.GuestIP)
	if guestIP != nil {
		route := &netlink.Route{
			Dst:       &net.IPNet{IP: guestIP, Mask: net.CIDRMask(32, 32)},
			LinkIndex: link.Attrs().Index,
		}
		if err := netlink.RouteDel(route); err != nil {
			slog.Warn("failed to delete guest route", "error", err)
		}
	}

	if err := netlink.LinkDel(link); err != nil {
		return fmt.Errorf("delete TAP %s: %w", cfg.TAPName, err)
	}

	slog.Info("routed TAP teardown complete", "tap", cfg.TAPName)
	return nil
}

// addDNSRule adds an iptables DNAT rule to redirect DNS from the TAP to
// ztunnel's DNS proxy at 127.0.0.1:15053.
func addDNSRule(tapName string) error {
	return iptables("-A", "ISTIO_PRERT", "-t", "nat",
		"-i", tapName, "-p", "udp", "--dport", "53",
		"-j", "DNAT", "--to-destination", fmt.Sprintf("127.0.0.1:%d", ztunnelDNSPort))
}

// removeDNSRule removes the DNS DNAT rule added by addDNSRule.
func removeDNSRule(tapName string) error {
	return iptables("-D", "ISTIO_PRERT", "-t", "nat",
		"-i", tapName, "-p", "udp", "--dport", "53",
		"-j", "DNAT", "--to-destination", fmt.Sprintf("127.0.0.1:%d", ztunnelDNSPort))
}

// iptables runs an iptables-legacy command. We use iptables-legacy because
// istio-cni uses legacy tables on our EKS cluster (nftables is available but
// istio's rules are in the legacy tables).
func iptables(args ...string) error {
	cmd := exec.Command("iptables-legacy", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("iptables-legacy %v: %s: %w", args, string(out), err)
	}
	return nil
}
