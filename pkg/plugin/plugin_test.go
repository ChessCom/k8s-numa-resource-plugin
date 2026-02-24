package plugin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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

func TestSetRegistrationTimeout(t *testing.T) {
	p := NewPlugin(0, "/tmp/test-sockets", 100)
	original := p.registrationTimeout

	p.SetRegistrationTimeout(0)
	if p.registrationTimeout != original {
		t.Errorf("expected zero duration to be ignored, timeout changed to %s", p.registrationTimeout)
	}

	p.SetRegistrationTimeout(-time.Second)
	if p.registrationTimeout != original {
		t.Errorf("expected negative duration to be ignored, timeout changed to %s", p.registrationTimeout)
	}

	p.SetRegistrationTimeout(30 * time.Second)
	if p.registrationTimeout != 30*time.Second {
		t.Errorf("expected timeout to be updated to 30s, got %s", p.registrationTimeout)
	}
}

// shortTempDir returns a temp dir short enough to hold a Unix socket path;
// t.TempDir() nests under a per-test name that overflows sockaddr_un's
// ~104-byte limit on macOS/BSD.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "numa-test-")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// newUnregisterablePlugin returns a plugin whose kubelet socket points at an
// empty temp dir, so registration can never succeed. Pointing it away from the
// real /var/lib/kubelet/device-plugins keeps these tests hermetic: otherwise
// they only pass on hosts that happen not to be running kubelet, and would
// fail outright when run on an actual Kubernetes node.
func newUnregisterablePlugin(t *testing.T, numaNodeID int) *NUMAResourcePlugin {
	t.Helper()
	p := NewPlugin(numaNodeID, shortTempDir(t), 1)
	p.kubeletSocketPath = filepath.Join(shortTempDir(t), KubeletSocket)
	return p
}

func TestTeardownServerRemovesSocket(t *testing.T) {
	dir := shortTempDir(t)
	p := NewPlugin(0, dir, 1)

	if err := p.serve(context.Background()); err != nil {
		t.Fatalf("serve() returned error: %v", err)
	}
	if _, err := os.Stat(p.socketPath); err != nil {
		t.Fatalf("expected socket file to exist after serve(), stat error: %v", err)
	}

	p.teardownServer()

	if _, err := os.Stat(p.socketPath); !os.IsNotExist(err) {
		t.Errorf("expected socket file to be removed after teardownServer(), stat error: %v", err)
	}
	p.mu.Lock()
	server := p.server
	p.mu.Unlock()
	if server != nil {
		t.Error("expected server to be nil after teardownServer()")
	}
}

// TestStartGivesUpAfterRegistrationTimeout exercises the real failure this
// bugfix targets: kubelet is unreachable, so registration can never succeed.
// Start must give up once registrationTimeout elapses instead of hanging or
// leaving a live server behind, and must remove the socket file it created
// along the way.
func TestStartGivesUpAfterRegistrationTimeout(t *testing.T) {
	p := newUnregisterablePlugin(t, 42)
	p.SetRegistrationTimeout(2 * time.Second)

	err := p.Start()
	if err == nil {
		t.Fatal("expected Start() to fail since no kubelet is listening")
	}
	t.Logf("Start() failed as expected: %v", err)

	if _, statErr := os.Stat(p.socketPath); !os.IsNotExist(statErr) {
		t.Errorf("expected socket file to be removed after giving up, stat error: %v", statErr)
	}
	p.mu.Lock()
	server := p.server
	p.mu.Unlock()
	if server != nil {
		t.Error("expected server to be nil after giving up")
	}
}

// TestWatchKubeletCallsOnFatalWhenRestartFails covers the recovery-path gap:
// if kubelet restarts and re-registration then fails, watchKubelet must
// report it via onFatal instead of retrying Restart() forever in the
// background with no way for the caller to ever notice.
func TestWatchKubeletCallsOnFatalWhenRestartFails(t *testing.T) {
	p := newUnregisterablePlugin(t, 7)
	p.SetRegistrationTimeout(2 * time.Second)

	fatalCh := make(chan error, 1)
	p.SetOnFatal(func(err error) { fatalCh <- err })

	// No socket exists yet (Start was never called), so watchKubelet's first
	// check immediately drives it into the Restart() path, which fails since
	// no kubelet is listening.
	p.watchKubelet()

	select {
	case err := <-fatalCh:
		if err == nil {
			t.Fatal("expected onFatal to be called with a non-nil error")
		}
		t.Logf("onFatal called with: %v", err)
	default:
		t.Fatal("expected onFatal to be called before watchKubelet returned")
	}

	if _, statErr := os.Stat(p.socketPath); !os.IsNotExist(statErr) {
		t.Errorf("expected socket file to be removed after giving up, stat error: %v", statErr)
	}
}

// TestRegisterWithRetryRecoversFromServeFailure is the regression test for the
// retry loop poisoning itself. A failed attempt that is not torn down leaves
// the socket path occupied, so every later serve() dies with EADDRINUSE and
// the registration budget is guaranteed to expire even when kubelet is healthy.
//
// The exposure is only ever a socket the loop created itself: Start() calls
// cleanup() before the first serve(), so a socket left behind by a previously
// crashed process is already handled and never reaches this path. Planting a
// file here is just the cheapest way to fail the first bind from outside the
// loop; what is being asserted is that the loop recovers from a failed serve()
// rather than being wedged by it, so later attempts get as far as kubelet
// registration. registerWithRetry is called directly, not Start(), precisely so
// that cleanup() does not remove the planted file first.
func TestRegisterWithRetryRecoversFromServeFailure(t *testing.T) {
	p := newUnregisterablePlugin(t, 3)
	p.SetRegistrationTimeout(3 * time.Second)

	if err := os.WriteFile(p.socketPath, nil, 0o600); err != nil {
		t.Fatalf("failed to occupy the socket path: %v", err)
	}

	err := p.registerWithRetry()
	if err == nil {
		t.Fatal("expected registration to fail since no kubelet is listening")
	}
	if strings.Contains(err.Error(), "failed to start server") {
		t.Errorf("retry loop never recovered from a failed serve(): the socket path is still "+
			"occupied on the final attempt, so every retry was wedged: %v", err)
	}
	if !strings.Contains(err.Error(), "failed to register") {
		t.Errorf("expected the final error to be a kubelet registration failure, got: %v", err)
	}
}

// TestServeSurvivesConcurrentTeardown is the regression test for the nil-server
// crash: serve()'s goroutine must use its own server reference, not re-read
// p.server, which teardownServer clears under the lock. Re-reading races with
// teardown and dereferences nil, panicking the whole process. Run with -race.
func TestServeSurvivesConcurrentTeardown(t *testing.T) {
	dir := shortTempDir(t)

	for i := 0; i < 50; i++ {
		p := NewPlugin(i, dir, 1)

		// A concurrent teardown pulls the socket out from under serve()'s
		// readiness dial, which would otherwise block for the full dialTimeout.
		// The race being tested is in serve()'s first few instructions, well
		// before the dial, so cutting the dial short costs no coverage.
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)

		// Hammer teardownServer while serve() is standing the server up: the
		// window between `p.server = server` and the serve goroutine reading it
		// is a few instructions wide, so a single concurrent teardown almost
		// always loses the race and the bug slips through.
		done := make(chan struct{})
		go func() {
			defer close(done)
			for j := 0; j < 100; j++ {
				p.teardownServer()
				runtime.Gosched()
			}
		}()
		_ = p.serve(ctx) // may fail; only the absence of a panic matters
		<-done

		p.teardownServer()
		cancel()
	}
}

// TestSetOnFatalIgnoresNil guards the invariant that an unrecoverable failure
// can never pass unnoticed: there is no way to install a no-op handler.
func TestSetOnFatalIgnoresNil(t *testing.T) {
	p := NewPlugin(0, "/tmp/test-sockets", 1)
	if p.onFatal == nil {
		t.Fatal("expected NewPlugin to install a default onFatal handler")
	}

	p.SetOnFatal(nil)
	if p.onFatal == nil {
		t.Error("expected SetOnFatal(nil) to be ignored, onFatal is now nil")
	}

	called := false
	p.SetOnFatal(func(error) { called = true })
	p.onFatal(nil)
	if !called {
		t.Error("expected SetOnFatal to install the supplied handler")
	}
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
