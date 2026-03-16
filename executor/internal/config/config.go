package config

import (
	"time"

	"github.com/caarlos0/env/v11"
)

// Config holds all executor configuration, loaded from environment variables.
type Config struct {
	ListenAddr       string        `env:"LISTEN_ADDR" envDefault:":9090"`
	PoolOperatorAddr string        `env:"POOL_OPERATOR_ADDR"`
	LeaseTTL         time.Duration `env:"LEASE_TTL" envDefault:"30s"`
	ImageDir         string        `env:"IMAGE_DIR" envDefault:"/opt/firecracker"`
	WorkloadDir      string        `env:"WORKLOAD_DIR" envDefault:"/workload"`
	VCPUs            int           `env:"VCPUS" envDefault:"1"`
	MemoryMB         int           `env:"MEMORY_MB" envDefault:"256"`
	BootTimeout      time.Duration `env:"BOOT_TIMEOUT" envDefault:"30s"`
	WarmTimeout      time.Duration `env:"WARM_TIMEOUT" envDefault:"5m"`
}

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
