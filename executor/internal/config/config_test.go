package config_test

import (
	"testing"
	"time"

	"github.com/siyanzhu/agentic-platform/executor/internal/config"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.ListenAddr != ":9090" {
		t.Errorf("ListenAddr = %q, want %q", cfg.ListenAddr, ":9090")
	}
	if cfg.BootTimeout != 30*time.Second {
		t.Errorf("BootTimeout = %v, want 30s", cfg.BootTimeout)
	}
	if cfg.ImageDir != "/opt/firecracker" {
		t.Errorf("ImageDir = %q, want %q", cfg.ImageDir, "/opt/firecracker")
	}
	if cfg.WorkloadDir != "/workload" {
		t.Errorf("WorkloadDir = %q, want %q", cfg.WorkloadDir, "/workload")
	}
}

func TestLoadFromEnv(t *testing.T) {
	t.Setenv("LISTEN_ADDR", ":8080")
	t.Setenv("POOL_OPERATOR_ADDR", "pool-op:8080")
	t.Setenv("LEASE_TTL", "60s")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %q, want %q", cfg.ListenAddr, ":8080")
	}
	if cfg.PoolOperatorAddr != "pool-op:8080" {
		t.Errorf("PoolOperatorAddr = %q, want %q", cfg.PoolOperatorAddr, "pool-op:8080")
	}
	if cfg.LeaseTTL != 60*time.Second {
		t.Errorf("LeaseTTL = %v, want 60s", cfg.LeaseTTL)
	}
}

func TestLoadInvalidDuration(t *testing.T) {
	t.Setenv("LEASE_TTL", "not-a-duration")

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load() should fail with invalid LEASE_TTL")
	}
}
