package plugin

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

const (
	// ResourceNamePrefix is the prefix for NUMA resources
	ResourceNamePrefix = "numa-align/numa-"

	// KubeletSocketDir is the standard kubelet device plugin socket directory
	// This is where kubelet.sock lives, even on RKE2
	KubeletSocketDir = "/var/lib/kubelet/device-plugins"

	// KubeletSocket is the name of the kubelet registration socket
	KubeletSocket = "kubelet.sock"

	// SocketPrefix is the prefix for plugin sockets
	SocketPrefix = "numa-"

	// DefaultRegistrationTimeout bounds how long Start retries registering with
	// kubelet before giving up, if the caller doesn't override it via
	// SetRegistrationTimeout.
	//
	// Keep this below the DaemonSet's liveness-probe kill window so the process
	// exits on its own terms, with a clear log line, rather than being killed
	// mid-retry. That window is *not* periodSeconds × failureThreshold: three
	// consecutive failures 30s apart span 60s from the first failure to the
	// kill, and the socket can disappear right at a probe instant, so 60s is
	// the floor - 90s is only the best case. Add watchKubelet's poll interval
	// (up to 5s to even notice the socket is gone) and the budget must stay
	// under ~55s. 45s leaves 10s of margin.
	DefaultRegistrationTimeout = 45 * time.Second

	// dialTimeout bounds a single connect attempt to a unix socket.
	dialTimeout = 5 * time.Second

	// registerCallTimeout bounds the kubelet Register RPC itself. Without it a
	// kubelet that accepts the connection but never answers would hang the
	// attempt indefinitely, and registrationTimeout - which is only consulted
	// between attempts - would never get a chance to fire.
	registerCallTimeout = 5 * time.Second

	initialBackoff = time.Second
	maxBackoff     = 2 * time.Minute
)

// NUMAResourcePlugin implements the Kubernetes Device Plugin interface
type NUMAResourcePlugin struct {
	resourceName        string
	numaNodeID          int
	capacity            int
	socketDir           string
	socketPath          string
	kubeletSocketPath   string
	server              *grpc.Server
	registrationTimeout time.Duration
	onFatal             func(error)

	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
}

// NewPlugin creates a new NUMA resource plugin for the given NUMA node.
//
// The plugin defaults to terminating the process on an unrecoverable
// registration failure; SetOnFatal replaces that with a caller-supplied
// handler. There is deliberately no "do nothing" option: a plugin that
// quietly stops watching is exactly the silent NUMA registration gap this
// design exists to prevent.
func NewPlugin(numaNodeID int, socketDir string, capacity int) *NUMAResourcePlugin {
	socketName := fmt.Sprintf("%s%d.sock", SocketPrefix, numaNodeID)
	ctx, cancel := context.WithCancel(context.Background())
	return &NUMAResourcePlugin{
		resourceName:        fmt.Sprintf("%s%d", ResourceNamePrefix, numaNodeID),
		numaNodeID:          numaNodeID,
		capacity:            capacity,
		socketDir:           socketDir,
		socketPath:          filepath.Join(socketDir, socketName),
		kubeletSocketPath:   filepath.Join(KubeletSocketDir, KubeletSocket),
		registrationTimeout: DefaultRegistrationTimeout,
		onFatal: func(err error) {
			log.Fatalf("[NUMA-%d] Unrecoverable registration failure and no onFatal handler installed: %v",
				numaNodeID, err)
		},
		ctx:    ctx,
		cancel: cancel,
	}
}

// SetRegistrationTimeout overrides how long Start retries registering with
// kubelet before giving up. Must be called before Start.
func (p *NUMAResourcePlugin) SetRegistrationTimeout(d time.Duration) {
	if d > 0 {
		p.registrationTimeout = d
	}
}

// SetOnFatal registers a callback invoked when the plugin permanently fails
// to re-register with kubelet after a successful start (e.g. after a kubelet
// restart, once registrationTimeout is exhausted). A partially-registered
// node is worse than a dead one, so this is the caller's hook to shut the
// whole process down loudly rather than let the node quietly run degraded.
// Must be called before Start.
//
// A nil callback is ignored: the default handler installed by NewPlugin stays
// in place so an unrecoverable failure can never pass unnoticed.
func (p *NUMAResourcePlugin) SetOnFatal(f func(error)) {
	if f != nil {
		p.onFatal = f
	}
}

// Start starts the gRPC server and registers with kubelet, retrying with
// backoff until it succeeds or registrationTimeout elapses.
func (p *NUMAResourcePlugin) Start() error {
	log.Printf("[NUMA-%d] Starting plugin for resource %s", p.numaNodeID, p.resourceName)

	// Remove existing socket if present
	if err := p.cleanup(); err != nil {
		return fmt.Errorf("failed to cleanup: %w", err)
	}

	if err := p.registerWithRetry(); err != nil {
		return err
	}

	log.Printf("[NUMA-%d] Plugin started successfully", p.numaNodeID)

	// Watch for kubelet restart
	go p.watchKubelet()

	return nil
}

// registerWithRetry starts the gRPC server and attempts to register with
// kubelet, retrying with exponential backoff on failure. A failed attempt
// tears down the server and socket before retrying, so a socket only ever
// exists while a server behind it is live or actively trying to register.
// Gives up once registrationTimeout elapses (or the plugin's context is
// cancelled), returning the last error encountered.
func (p *NUMAResourcePlugin) registerWithRetry() error {
	p.mu.Lock()
	ctx := p.ctx
	p.mu.Unlock()

	deadlineCtx, cancel := context.WithTimeout(ctx, p.registrationTimeout)
	defer cancel()

	backoff := initialBackoff
	for {
		err := p.attemptRegistration(deadlineCtx)
		if err == nil {
			return nil
		}

		// Every failed attempt is torn down here, in one place. Doing it per
		// failure branch inside attemptRegistration invites the branches to
		// drift apart, and a missed teardown leaves the listener holding the
		// socket path - which poisons every later retry with EADDRINUSE.
		p.teardownServer()

		log.Printf("[NUMA-%d] Registration attempt failed, retrying in %s: %v", p.numaNodeID, backoff, err)

		select {
		case <-deadlineCtx.Done():
			if errors.Is(deadlineCtx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("giving up after %s of retries: %w", p.registrationTimeout, err)
			}
			return fmt.Errorf("registration cancelled: %w", err)
		case <-time.After(backoff):
			backoff = min(backoff*2, maxBackoff)
		}
	}
}

// attemptRegistration makes a single attempt to stand up the gRPC server and
// register it with kubelet. On any failure the caller must tear the server
// down before retrying; see registerWithRetry.
func (p *NUMAResourcePlugin) attemptRegistration(ctx context.Context) error {
	if err := p.serve(ctx); err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}
	if err := p.register(ctx); err != nil {
		return fmt.Errorf("failed to register: %w", err)
	}
	return nil
}

// teardownServer stops the gRPC server, if running, and removes the socket.
func (p *NUMAResourcePlugin) teardownServer() {
	p.mu.Lock()
	server := p.server
	p.server = nil
	p.mu.Unlock()
	if server != nil {
		server.Stop()
	}
	p.cleanup()
}

// Stop stops the plugin
func (p *NUMAResourcePlugin) Stop() {
	log.Printf("[NUMA-%d] Stopping plugin", p.numaNodeID)
	p.mu.Lock()
	p.cancel()
	p.mu.Unlock()
	p.teardownServer()
}

// serve starts the gRPC server
func (p *NUMAResourcePlugin) serve(ctx context.Context) error {
	listener, err := net.Listen("unix", p.socketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", p.socketPath, err)
	}

	server := grpc.NewServer()
	pluginapi.RegisterDevicePluginServer(server, p)

	p.mu.Lock()
	p.server = server
	p.mu.Unlock()

	// Serve the local `server`, never p.server: teardownServer clears that
	// field under the lock, so re-reading it here races with teardown and can
	// dereference nil, panicking the whole process.
	go func() {
		if err := server.Serve(listener); err != nil {
			log.Printf("[NUMA-%d] gRPC server error: %v", p.numaNodeID, err)
		}
	}()

	// Wait for server to be ready
	conn, err := p.dial(ctx, p.socketPath, dialTimeout)
	if err != nil {
		return fmt.Errorf("failed to connect to plugin socket: %w", err)
	}
	conn.Close()

	log.Printf("[NUMA-%d] gRPC server listening on %s", p.numaNodeID, p.socketPath)
	return nil
}

// register registers the plugin with kubelet
func (p *NUMAResourcePlugin) register(ctx context.Context) error {
	conn, err := p.dial(ctx, p.kubeletSocketPath, dialTimeout)
	if err != nil {
		return fmt.Errorf("failed to connect to kubelet: %w", err)
	}
	defer conn.Close()

	client := pluginapi.NewRegistrationClient(conn)
	req := &pluginapi.RegisterRequest{
		Version:      pluginapi.Version,
		Endpoint:     filepath.Base(p.socketPath),
		ResourceName: p.resourceName,
		Options: &pluginapi.DevicePluginOptions{
			PreStartRequired:                false,
			GetPreferredAllocationAvailable: true,
		},
	}

	regCtx, cancel := context.WithTimeout(ctx, registerCallTimeout)
	defer cancel()

	_, err = client.Register(regCtx, req)
	if err != nil {
		return fmt.Errorf("failed to register with kubelet: %w", err)
	}

	log.Printf("[NUMA-%d] Registered with kubelet as %s", p.numaNodeID, p.resourceName)
	return nil
}

// watchKubelet watches for kubelet restarts and re-registers. Restart already
// retries registration internally with its own bounded backoff, so a failure
// here means that budget is exhausted - retrying again in an outer loop would
// just silently prolong a degraded, unregistered node. Instead this reports
// the failure via onFatal (if set) and stops watching.
func (p *NUMAResourcePlugin) watchKubelet() {
	for {
		p.mu.Lock()
		ctx := p.ctx
		p.mu.Unlock()

		select {
		case <-ctx.Done():
			return
		default:
		}

		// Check if our socket still exists
		if _, err := os.Stat(p.socketPath); os.IsNotExist(err) {
			log.Printf("[NUMA-%d] Socket removed, restarting...", p.numaNodeID)
			if err := p.Restart(); err != nil {
				log.Printf("[NUMA-%d] Failed to re-register after kubelet restart, giving up: %v", p.numaNodeID, err)
				// onFatal is never nil: NewPlugin installs a process-exiting
				// default and SetOnFatal refuses to replace it with nil.
				p.onFatal(fmt.Errorf("NUMA node %d: %w", p.numaNodeID, err))
			}
			return // Exit this goroutine — Restart spawned a new one on success
		}

		// Check if kubelet socket exists (kubelet might have restarted)
		if _, err := os.Stat(p.kubeletSocketPath); os.IsNotExist(err) {
			log.Printf("[NUMA-%d] Kubelet socket missing, waiting...", p.numaNodeID)
			time.Sleep(time.Second)
			continue
		}

		time.Sleep(5 * time.Second)
	}
}

// Restart restarts the plugin (re-register with kubelet)
func (p *NUMAResourcePlugin) Restart() error {
	log.Printf("[NUMA-%d] Restarting plugin", p.numaNodeID)

	p.mu.Lock()
	// Stop the gRPC server
	server := p.server
	p.server = nil
	// Cancel old context and create a new one
	p.cancel()
	p.ctx, p.cancel = context.WithCancel(context.Background())
	p.mu.Unlock()
	if server != nil {
		server.Stop()
	}

	p.cleanup()
	return p.Start()
}

// cleanup removes the plugin socket
func (p *NUMAResourcePlugin) cleanup() error {
	if err := os.Remove(p.socketPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// dial creates a gRPC connection to the given socket. The connect attempt is
// bounded by timeout and by the caller's ctx, whichever expires first, so an
// exhausted registration budget aborts an in-flight dial instead of outliving it.
func (p *NUMAResourcePlugin) dial(ctx context.Context, socket string, timeout time.Duration) (*grpc.ClientConn, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	conn, err := grpc.DialContext(ctx, "unix://"+socket,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

// GetDevicePluginOptions returns the device plugin options
func (p *NUMAResourcePlugin) GetDevicePluginOptions(ctx context.Context, empty *pluginapi.Empty) (*pluginapi.DevicePluginOptions, error) {
	return &pluginapi.DevicePluginOptions{
		PreStartRequired:                false,
		GetPreferredAllocationAvailable: true,
	}, nil
}

// ListAndWatch lists devices and watches for changes
func (p *NUMAResourcePlugin) ListAndWatch(empty *pluginapi.Empty, stream pluginapi.DevicePlugin_ListAndWatchServer) error {
	log.Printf("[NUMA-%d] ListAndWatch called, advertising %d devices", p.numaNodeID, p.capacity)

	// Capture context under lock for race safety
	p.mu.Lock()
	ctx := p.ctx
	p.mu.Unlock()

	// Create devices based on configured capacity
	devices := make([]*pluginapi.Device, p.capacity)
	for i := 0; i < p.capacity; i++ {
		devices[i] = &pluginapi.Device{
			ID:     fmt.Sprintf("numa-%d-slot-%d", p.numaNodeID, i),
			Health: pluginapi.Healthy,
			Topology: &pluginapi.TopologyInfo{
				Nodes: []*pluginapi.NUMANode{
					{ID: int64(p.numaNodeID)},
				},
			},
		}
	}

	response := &pluginapi.ListAndWatchResponse{
		Devices: devices,
	}

	if err := stream.Send(response); err != nil {
		return fmt.Errorf("failed to send device list: %w", err)
	}

	// Keep connection open until stopped
	<-ctx.Done()
	return nil
}

// Allocate allocates devices for a container
func (p *NUMAResourcePlugin) Allocate(ctx context.Context, req *pluginapi.AllocateRequest) (*pluginapi.AllocateResponse, error) {
	log.Printf("[NUMA-%d] Allocate called for %d container(s)", p.numaNodeID, len(req.ContainerRequests))

	responses := make([]*pluginapi.ContainerAllocateResponse, len(req.ContainerRequests))
	for i, containerReq := range req.ContainerRequests {
		log.Printf("[NUMA-%d] Container %d requesting devices: %v", p.numaNodeID, i, containerReq.DevicesIDs)
		responses[i] = &pluginapi.ContainerAllocateResponse{
			Envs: map[string]string{
				"NUMA_NODE": fmt.Sprintf("%d", p.numaNodeID),
			},
		}
	}

	return &pluginapi.AllocateResponse{ContainerResponses: responses}, nil
}

// GetPreferredAllocation returns the preferred allocation for the given request
func (p *NUMAResourcePlugin) GetPreferredAllocation(ctx context.Context, req *pluginapi.PreferredAllocationRequest) (*pluginapi.PreferredAllocationResponse, error) {
	log.Printf("[NUMA-%d] GetPreferredAllocation called", p.numaNodeID)

	responses := make([]*pluginapi.ContainerPreferredAllocationResponse, len(req.ContainerRequests))
	for i, containerReq := range req.ContainerRequests {
		// Prefer the first N available devices
		preferred := containerReq.AvailableDeviceIDs
		if int(containerReq.AllocationSize) < len(preferred) {
			preferred = preferred[:containerReq.AllocationSize]
		}
		responses[i] = &pluginapi.ContainerPreferredAllocationResponse{
			DeviceIDs: preferred,
		}
	}

	return &pluginapi.PreferredAllocationResponse{ContainerResponses: responses}, nil
}

// PreStartContainer is called before a container starts
func (p *NUMAResourcePlugin) PreStartContainer(ctx context.Context, req *pluginapi.PreStartContainerRequest) (*pluginapi.PreStartContainerResponse, error) {
	// No-op for this plugin
	return &pluginapi.PreStartContainerResponse{}, nil
}
