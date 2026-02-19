package config

import (
	"os"
	"strconv"
)

// Config holds the plugin configuration
type Config struct {
	SocketDir string // Device plugin socket directory
	Capacity  int    // Capacity per NUMA node (default: 100)
}

// Load loads configuration from environment variables
func Load() *Config {
	cfg := &Config{
		SocketDir: detectSocketDir(),
		Capacity:  100,
	}

	// Override socket dir from env
	if dir := os.Getenv("NUMA_SOCKET_DIR"); dir != "" {
		cfg.SocketDir = dir
	}

	// Override capacity from env
	if cap := os.Getenv("NUMA_CAPACITY"); cap != "" {
		if c, err := strconv.Atoi(cap); err == nil && c > 0 {
			cfg.Capacity = c
		}
	}

	return cfg
}

// detectSocketDir returns the kubelet device plugin socket directory
// This is always the standard path - kubelet expects device plugin sockets here
func detectSocketDir() string {
	// Always use the standard kubelet path
	// Even RKE2 uses this path for device plugin registration
	return "/var/lib/kubelet/device-plugins"
}
