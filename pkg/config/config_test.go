package config

import (
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		t.Setenv("NUMA_SOCKET_DIR", "")
		t.Setenv("NUMA_CAPACITY", "")
		t.Setenv("NUMA_REGISTRATION_TIMEOUT_SECONDS", "")

		cfg := Load()

		if cfg.SocketDir != "/var/lib/kubelet/device-plugins" {
			t.Errorf("Expected default socket dir, got %s", cfg.SocketDir)
		}
		if cfg.Capacity != 100 {
			t.Errorf("Expected capacity 100, got %d", cfg.Capacity)
		}
		// Zero means "no override"; the plugin package supplies the default.
		if cfg.RegistrationTimeout != 0 {
			t.Errorf("Expected no registration timeout override, got %s", cfg.RegistrationTimeout)
		}
	})

	// 37s is deliberately an odd value that matches no default anywhere, so this
	// case can only pass if the env var was actually parsed and applied.
	t.Run("custom registration timeout", func(t *testing.T) {
		t.Setenv("NUMA_REGISTRATION_TIMEOUT_SECONDS", "37")

		cfg := Load()

		if cfg.RegistrationTimeout != 37*time.Second {
			t.Errorf("Expected registration timeout 37s, got %s", cfg.RegistrationTimeout)
		}
	})

	t.Run("invalid registration timeout leaves no override", func(t *testing.T) {
		t.Setenv("NUMA_REGISTRATION_TIMEOUT_SECONDS", "invalid")

		cfg := Load()

		if cfg.RegistrationTimeout != 0 {
			t.Errorf("Expected no registration timeout override, got %s", cfg.RegistrationTimeout)
		}
	})

	t.Run("zero registration timeout leaves no override", func(t *testing.T) {
		t.Setenv("NUMA_REGISTRATION_TIMEOUT_SECONDS", "0")

		cfg := Load()

		if cfg.RegistrationTimeout != 0 {
			t.Errorf("Expected no registration timeout override, got %s", cfg.RegistrationTimeout)
		}
	})

	t.Run("custom socket dir", func(t *testing.T) {
		t.Setenv("NUMA_SOCKET_DIR", "/custom/path")
		t.Setenv("NUMA_CAPACITY", "")

		cfg := Load()

		if cfg.SocketDir != "/custom/path" {
			t.Errorf("Expected custom socket dir, got %s", cfg.SocketDir)
		}
	})

	t.Run("custom capacity", func(t *testing.T) {
		t.Setenv("NUMA_SOCKET_DIR", "")
		t.Setenv("NUMA_CAPACITY", "50")

		cfg := Load()

		if cfg.Capacity != 50 {
			t.Errorf("Expected capacity 50, got %d", cfg.Capacity)
		}
	})

	t.Run("invalid capacity falls back to default", func(t *testing.T) {
		t.Setenv("NUMA_SOCKET_DIR", "")
		t.Setenv("NUMA_CAPACITY", "invalid")

		cfg := Load()

		if cfg.Capacity != 100 {
			t.Errorf("Expected default capacity 100, got %d", cfg.Capacity)
		}
	})

	t.Run("zero capacity falls back to default", func(t *testing.T) {
		t.Setenv("NUMA_SOCKET_DIR", "")
		t.Setenv("NUMA_CAPACITY", "0")

		cfg := Load()

		if cfg.Capacity != 100 {
			t.Errorf("Expected default capacity 100, got %d", cfg.Capacity)
		}
	})

	t.Run("negative capacity falls back to default", func(t *testing.T) {
		t.Setenv("NUMA_SOCKET_DIR", "")
		t.Setenv("NUMA_CAPACITY", "-10")

		cfg := Load()

		if cfg.Capacity != 100 {
			t.Errorf("Expected default capacity 100, got %d", cfg.Capacity)
		}
	})

}

func TestDetectSocketDir(t *testing.T) {
	dir := detectSocketDir()
	expected := "/var/lib/kubelet/device-plugins"
	if dir != expected {
		t.Errorf("Expected %s, got %s", expected, dir)
	}
}
