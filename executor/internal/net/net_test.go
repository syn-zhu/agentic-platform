package net_test

import (
	"testing"

	execnet "github.com/siyanzhu/agentic-platform/executor/internal/net"
)

func TestDefaultConfig(t *testing.T) {
	cfg := &execnet.Config{
		TAPName: execnet.DefaultTAPName,
		HostIP:  execnet.DefaultHostIP,
		GuestIP: execnet.DefaultGuestIP,
		MTU:     1500,
	}

	if cfg.TAPName != "fctr-tap0" {
		t.Errorf("TAPName = %q, want %q", cfg.TAPName, "fctr-tap0")
	}
	if cfg.HostIP != "169.254.1.1" {
		t.Errorf("HostIP = %q, want %q", cfg.HostIP, "169.254.1.1")
	}
	if cfg.GuestIP != "169.254.1.2" {
		t.Errorf("GuestIP = %q, want %q", cfg.GuestIP, "169.254.1.2")
	}
}

func TestGuestNetworkConfig(t *testing.T) {
	cfg := &execnet.Config{
		TAPName: execnet.DefaultTAPName,
		HostIP:  execnet.DefaultHostIP,
		GuestIP: execnet.DefaultGuestIP,
		MTU:     9001,
	}

	gc := cfg.GuestNetworkConfig()
	if gc.IP != "169.254.1.2" {
		t.Errorf("IP = %q, want %q", gc.IP, "169.254.1.2")
	}
	if gc.Gateway != "169.254.1.1" {
		t.Errorf("Gateway = %q, want %q", gc.Gateway, "169.254.1.1")
	}
	if gc.PrefixLen != 32 {
		t.Errorf("PrefixLen = %d, want 32", gc.PrefixLen)
	}
	if gc.MTU != 9001 {
		t.Errorf("MTU = %d, want 9001", gc.MTU)
	}
}

// Setup and Teardown require NET_ADMIN capability and cannot be unit tested
// in a normal environment. They were validated on the live cluster in Phase 1.
// See executor/test/networking/ for the cluster validation scripts.
