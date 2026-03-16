package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

const (
	// Fixed container filesystem paths — set by the pod template, not configurable.
	ImageDir    = "/opt/firecracker"
	WorkloadDir = "/workload"
	AgentPort   = 8080
)

// Config holds all executor configuration.
type Config struct {
	ListenAddr       string
	PoolOperatorAddr string
	LeaseTTL         time.Duration
	VCPUs            int
	Memory           string
	BootTimeout      time.Duration
	ReadyTimeout     time.Duration
	ExecTimeout      time.Duration
}

// Load reads configuration from environment variables with defaults.
func Load() (*Config, error) {
	cfg := &Config{
		ListenAddr:       envOr("LISTEN_ADDR", ":9090"),
		PoolOperatorAddr: os.Getenv("POOL_OPERATOR_ADDR"),
		VCPUs:            envIntOr("VCPUS", 1),
		Memory:           envOr("MEMORY", "256M"),
	}

	var err error
	cfg.LeaseTTL, err = envDurationOr("LEASE_TTL", 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("LEASE_TTL: %w", err)
	}
	cfg.BootTimeout, err = envDurationOr("BOOT_TIMEOUT", 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("BOOT_TIMEOUT: %w", err)
	}
	cfg.ReadyTimeout, err = envDurationOr("READY_TIMEOUT", 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("READY_TIMEOUT: %w", err)
	}
	cfg.ExecTimeout, err = envDurationOr("EXEC_TIMEOUT", 5*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("EXEC_TIMEOUT: %w", err)
	}

	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envIntOr(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func envDurationOr(key string, fallback time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	return time.ParseDuration(v)
}
