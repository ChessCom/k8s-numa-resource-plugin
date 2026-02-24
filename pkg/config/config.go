package config

import (
	"os"
	"strconv"
	"time"
)

// Config holds the plugin configuration
type Config struct {
	SocketDir string // Device plugin socket directory
	Capacity  int    // Capacity per NUMA node (default: 100)

	// RegistrationTimeout is the operator's override for how long to retry
	// kubelet registration before giving up. Zero means "no override": the
	// plugin package owns that default, and this package deliberately does not
	// restate it - a second copy of the value is a second thing to drift.
	RegistrationTimeout time.Duration
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

	// Override registration timeout (seconds) from env. Anything unset, invalid
	// or non-positive leaves this zero, and the plugin default applies.
	if secs := os.Getenv("NUMA_REGISTRATION_TIMEOUT_SECONDS"); secs != "" {
		if s, err := strconv.Atoi(secs); err == nil && s > 0 {
			cfg.RegistrationTimeout = time.Duration(s) * time.Second
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
