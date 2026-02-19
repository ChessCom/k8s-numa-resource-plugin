package plugin

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

func TestNewPlugin(t *testing.T) {
	p := NewPlugin(0, "/tmp/test-sockets", 100)

	if p.resourceName != "numa-align/numa-0" {
		t.Errorf("Expected resource name numa-align/numa-0, got %s", p.resourceName)
	}
	if p.numaNodeID != 0 {
		t.Errorf("Expected NUMA node ID 0, got %d", p.numaNodeID)
	}
	if p.capacity != 100 {
		t.Errorf("Expected capacity 100, got %d", p.capacity)
	}
	if p.socketPath != filepath.Join("/tmp/test-sockets", "numa-0.sock") {
		t.Errorf("Expected socket path /tmp/test-sockets/numa-0.sock, got %s", p.socketPath)
	}

	t.Run("different NUMA node", func(t *testing.T) {
		p := NewPlugin(3, "/var/lib/kubelet/device-plugins", 50)
		if p.resourceName != "numa-align/numa-3" {
			t.Errorf("Expected resource name numa-align/numa-3, got %s", p.resourceName)
		}
		if p.socketPath != "/var/lib/kubelet/device-plugins/numa-3.sock" {
			t.Errorf("Expected socket path ending in numa-3.sock, got %s", p.socketPath)
		}
	})
}

func TestAllocate(t *testing.T) {
	p := NewPlugin(0, "/tmp/test-sockets", 100)

	t.Run("single container", func(t *testing.T) {
		req := &pluginapi.AllocateRequest{
			ContainerRequests: []*pluginapi.ContainerAllocateRequest{
				{DevicesIDs: []string{"numa-0-slot-0"}},
			},
		}

		resp, err := p.Allocate(context.Background(), req)
		if err != nil {
			t.Fatalf("Allocate returned error: %v", err)
		}
		if len(resp.ContainerResponses) != 1 {
			t.Fatalf("Expected 1 response, got %d", len(resp.ContainerResponses))
		}

		numaNode, ok := resp.ContainerResponses[0].Envs["NUMA_NODE"]
		if !ok {
			t.Fatal("NUMA_NODE env var not set")
		}
		if numaNode != "0" {
			t.Errorf("Expected NUMA_NODE=0, got %s", numaNode)
		}
	})

	t.Run("multiple containers", func(t *testing.T) {
		req := &pluginapi.AllocateRequest{
			ContainerRequests: []*pluginapi.ContainerAllocateRequest{
				{DevicesIDs: []string{"numa-0-slot-0"}},
				{DevicesIDs: []string{"numa-0-slot-1", "numa-0-slot-2"}},
			},
		}

		resp, err := p.Allocate(context.Background(), req)
		if err != nil {
			t.Fatalf("Allocate returned error: %v", err)
		}
		if len(resp.ContainerResponses) != 2 {
			t.Fatalf("Expected 2 responses, got %d", len(resp.ContainerResponses))
		}

		for i, cr := range resp.ContainerResponses {
			numaNode, ok := cr.Envs["NUMA_NODE"]
			if !ok {
				t.Errorf("Container %d: NUMA_NODE env var not set", i)
			}
			if numaNode != "0" {
				t.Errorf("Container %d: Expected NUMA_NODE=0, got %s", i, numaNode)
			}
		}
	})

	t.Run("different NUMA node", func(t *testing.T) {
		p := NewPlugin(2, "/tmp/test-sockets", 100)
		req := &pluginapi.AllocateRequest{
			ContainerRequests: []*pluginapi.ContainerAllocateRequest{
				{DevicesIDs: []string{"numa-2-slot-0"}},
			},
		}

		resp, err := p.Allocate(context.Background(), req)
		if err != nil {
			t.Fatalf("Allocate returned error: %v", err)
		}

		numaNode := resp.ContainerResponses[0].Envs["NUMA_NODE"]
		if numaNode != "2" {
			t.Errorf("Expected NUMA_NODE=2, got %s", numaNode)
		}
	})
}

func TestGetPreferredAllocation(t *testing.T) {
	p := NewPlugin(0, "/tmp/test-sockets", 100)

	t.Run("allocation size less than available", func(t *testing.T) {
		req := &pluginapi.PreferredAllocationRequest{
			ContainerRequests: []*pluginapi.ContainerPreferredAllocationRequest{
				{
					AvailableDeviceIDs: []string{"numa-0-slot-0", "numa-0-slot-1", "numa-0-slot-2"},
					AllocationSize:     2,
				},
			},
		}

		resp, err := p.GetPreferredAllocation(context.Background(), req)
		if err != nil {
			t.Fatalf("GetPreferredAllocation returned error: %v", err)
		}
		if len(resp.ContainerResponses) != 1 {
			t.Fatalf("Expected 1 response, got %d", len(resp.ContainerResponses))
		}
		if len(resp.ContainerResponses[0].DeviceIDs) != 2 {
			t.Errorf("Expected 2 preferred devices, got %d", len(resp.ContainerResponses[0].DeviceIDs))
		}
		if resp.ContainerResponses[0].DeviceIDs[0] != "numa-0-slot-0" {
			t.Errorf("Expected first preferred device numa-0-slot-0, got %s", resp.ContainerResponses[0].DeviceIDs[0])
		}
	})

	t.Run("allocation size equals available", func(t *testing.T) {
		req := &pluginapi.PreferredAllocationRequest{
			ContainerRequests: []*pluginapi.ContainerPreferredAllocationRequest{
				{
					AvailableDeviceIDs: []string{"numa-0-slot-0", "numa-0-slot-1"},
					AllocationSize:     2,
				},
			},
		}

		resp, err := p.GetPreferredAllocation(context.Background(), req)
		if err != nil {
			t.Fatalf("GetPreferredAllocation returned error: %v", err)
		}
		if len(resp.ContainerResponses[0].DeviceIDs) != 2 {
			t.Errorf("Expected 2 preferred devices, got %d", len(resp.ContainerResponses[0].DeviceIDs))
		}
	})

	t.Run("allocation size exceeds available", func(t *testing.T) {
		req := &pluginapi.PreferredAllocationRequest{
			ContainerRequests: []*pluginapi.ContainerPreferredAllocationRequest{
				{
					AvailableDeviceIDs: []string{"numa-0-slot-0"},
					AllocationSize:     5,
				},
			},
		}

		resp, err := p.GetPreferredAllocation(context.Background(), req)
		if err != nil {
			t.Fatalf("GetPreferredAllocation returned error: %v", err)
		}
		if len(resp.ContainerResponses[0].DeviceIDs) != 1 {
			t.Errorf("Expected 1 preferred device (all available), got %d", len(resp.ContainerResponses[0].DeviceIDs))
		}
	})
}

func TestGetDevicePluginOptions(t *testing.T) {
	p := NewPlugin(0, "/tmp/test-sockets", 100)

	opts, err := p.GetDevicePluginOptions(context.Background(), &pluginapi.Empty{})
	if err != nil {
		t.Fatalf("GetDevicePluginOptions returned error: %v", err)
	}
	if opts.PreStartRequired {
		t.Error("Expected PreStartRequired to be false")
	}
	if !opts.GetPreferredAllocationAvailable {
		t.Error("Expected GetPreferredAllocationAvailable to be true")
	}
}

func TestPreStartContainer(t *testing.T) {
	p := NewPlugin(0, "/tmp/test-sockets", 100)

	resp, err := p.PreStartContainer(context.Background(), &pluginapi.PreStartContainerRequest{})
	if err != nil {
		t.Fatalf("PreStartContainer returned error: %v", err)
	}
	if resp == nil {
		t.Fatal("Expected non-nil response")
	}
}

// mockListAndWatchServer implements DevicePlugin_ListAndWatchServer for testing
type mockListAndWatchServer struct {
	pluginapi.DevicePlugin_ListAndWatchServer
	responses []*pluginapi.ListAndWatchResponse
}

func (m *mockListAndWatchServer) Send(resp *pluginapi.ListAndWatchResponse) error {
	m.responses = append(m.responses, resp)
	return nil
}

func (m *mockListAndWatchServer) Context() context.Context {
	return context.Background()
}

func TestListAndWatch(t *testing.T) {
	t.Run("device count matches capacity", func(t *testing.T) {
		p := NewPlugin(0, "/tmp/test-sockets", 10)
		mock := &mockListAndWatchServer{}

		// Cancel immediately so ListAndWatch doesn't block
		p.mu.Lock()
		p.cancel()
		p.mu.Unlock()

		err := p.ListAndWatch(&pluginapi.Empty{}, mock)
		if err != nil {
			t.Fatalf("ListAndWatch returned error: %v", err)
		}
		if len(mock.responses) != 1 {
			t.Fatalf("Expected 1 response, got %d", len(mock.responses))
		}
		if len(mock.responses[0].Devices) != 10 {
			t.Errorf("Expected 10 devices, got %d", len(mock.responses[0].Devices))
		}
	})

	t.Run("device IDs follow naming convention", func(t *testing.T) {
		p := NewPlugin(1, "/tmp/test-sockets", 3)
		mock := &mockListAndWatchServer{}

		p.mu.Lock()
		p.cancel()
		p.mu.Unlock()

		_ = p.ListAndWatch(&pluginapi.Empty{}, mock)

		for i, dev := range mock.responses[0].Devices {
			expected := fmt.Sprintf("numa-1-slot-%d", i)
			if dev.ID != expected {
				t.Errorf("Device %d: expected ID %s, got %s", i, expected, dev.ID)
			}
		}
	})

	t.Run("devices have correct topology info", func(t *testing.T) {
		p := NewPlugin(2, "/tmp/test-sockets", 5)
		mock := &mockListAndWatchServer{}

		p.mu.Lock()
		p.cancel()
		p.mu.Unlock()

		_ = p.ListAndWatch(&pluginapi.Empty{}, mock)

		for i, dev := range mock.responses[0].Devices {
			if dev.Topology == nil {
				t.Errorf("Device %d: topology is nil", i)
				continue
			}
			if len(dev.Topology.Nodes) != 1 {
				t.Errorf("Device %d: expected 1 NUMA node, got %d", i, len(dev.Topology.Nodes))
				continue
			}
			if dev.Topology.Nodes[0].ID != 2 {
				t.Errorf("Device %d: expected NUMA node ID 2, got %d", i, dev.Topology.Nodes[0].ID)
			}
		}
	})

	t.Run("all devices are healthy", func(t *testing.T) {
		p := NewPlugin(0, "/tmp/test-sockets", 5)
		mock := &mockListAndWatchServer{}

		p.mu.Lock()
		p.cancel()
		p.mu.Unlock()

		_ = p.ListAndWatch(&pluginapi.Empty{}, mock)

		for i, dev := range mock.responses[0].Devices {
			if dev.Health != pluginapi.Healthy {
				t.Errorf("Device %d: expected Healthy, got %s", i, dev.Health)
			}
		}
	})
}

func TestStopCancelsContext(t *testing.T) {
	p := NewPlugin(0, "/tmp/test-sockets", 10)

	p.mu.Lock()
	ctx := p.ctx
	p.mu.Unlock()

	p.Stop()

	select {
	case <-ctx.Done():
		// expected
	default:
		t.Error("Expected context to be cancelled after Stop()")
	}
}
