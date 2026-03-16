package image

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Config holds image metadata read from image-config.json inside the rootfs.
// The rootfs builder generates this from the Dockerfile metadata.
type Config struct {
	Entrypoint []string          `json:"entrypoint"`
	Port       int               `json:"port"`
	Env        map[string]string `json:"env"`
}

// LoadConfig reads image-config.json from the rootfs ext4 image.
// Since the rootfs is an ext4 image (not mounted on the host), we
// also store a copy of image-config.json alongside the rootfs in
// the image directory. The rootfs builder emits both:
//   - rootfs.ext4 (contains image-config.json for the guest init)
//   - image-config.json (same file, for the executor to read)
func LoadConfig(imageDir string) (*Config, error) {
	path := filepath.Join(imageDir, "image-config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	// Apply defaults.
	if cfg.Port == 0 {
		cfg.Port = 8080
	}

	if len(cfg.Entrypoint) == 0 {
		return nil, fmt.Errorf("entrypoint is required in %s", path)
	}

	return &cfg, nil
}
