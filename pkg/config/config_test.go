package config

import (
	"testing"
)

func TestLoad(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		t.Setenv("NUMA_SOCKET_DIR", "")
		t.Setenv("NUMA_CAPACITY", "")

		cfg := Load()

		if cfg.SocketDir != "/var/lib/kubelet/device-plugins" {
			t.Errorf("Expected default socket dir, got %s", cfg.SocketDir)
		}
		if cfg.Capacity != 100 {
			t.Errorf("Expected capacity 100, got %d", cfg.Capacity)
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
