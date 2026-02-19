package plugin

import (
	"context"
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
)

// NUMAResourcePlugin implements the Kubernetes Device Plugin interface
type NUMAResourcePlugin struct {
	resourceName string
	numaNodeID   int
	capacity     int
	socketDir    string
	socketPath   string
	server       *grpc.Server

	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
}

// NewPlugin creates a new NUMA resource plugin for the given NUMA node
func NewPlugin(numaNodeID int, socketDir string, capacity int) *NUMAResourcePlugin {
	socketName := fmt.Sprintf("%s%d.sock", SocketPrefix, numaNodeID)
	ctx, cancel := context.WithCancel(context.Background())
	return &NUMAResourcePlugin{
		resourceName: fmt.Sprintf("%s%d", ResourceNamePrefix, numaNodeID),
		numaNodeID:   numaNodeID,
		capacity:     capacity,
		socketDir:    socketDir,
		socketPath:   filepath.Join(socketDir, socketName),
		ctx:          ctx,
		cancel:       cancel,
	}
}

// Start starts the gRPC server and registers with kubelet
func (p *NUMAResourcePlugin) Start() error {
	log.Printf("[NUMA-%d] Starting plugin for resource %s", p.numaNodeID, p.resourceName)

	// Remove existing socket if present
	if err := p.cleanup(); err != nil {
		return fmt.Errorf("failed to cleanup: %w", err)
	}

	// Start gRPC server
	if err := p.serve(); err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}

	// Register with kubelet
	if err := p.register(); err != nil {
		return fmt.Errorf("failed to register: %w", err)
	}

	log.Printf("[NUMA-%d] Plugin started successfully", p.numaNodeID)

	// Watch for kubelet restart
	go p.watchKubelet()

	return nil
}

// Stop stops the plugin
func (p *NUMAResourcePlugin) Stop() {
	log.Printf("[NUMA-%d] Stopping plugin", p.numaNodeID)
	p.mu.Lock()
	p.cancel()
	server := p.server
	p.mu.Unlock()
	if server != nil {
		server.Stop()
	}
	p.cleanup()
}

// serve starts the gRPC server
func (p *NUMAResourcePlugin) serve() error {
	listener, err := net.Listen("unix", p.socketPath)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", p.socketPath, err)
	}

	server := grpc.NewServer()
	pluginapi.RegisterDevicePluginServer(server, p)

	p.mu.Lock()
	p.server = server
	p.mu.Unlock()

	go func() {
		if err := p.server.Serve(listener); err != nil {
			log.Printf("[NUMA-%d] gRPC server error: %v", p.numaNodeID, err)
		}
	}()

	// Wait for server to be ready
	conn, err := p.dial(p.socketPath, 5*time.Second)
	if err != nil {
		return fmt.Errorf("failed to connect to plugin socket: %w", err)
	}
	conn.Close()

	log.Printf("[NUMA-%d] gRPC server listening on %s", p.numaNodeID, p.socketPath)
	return nil
}

// register registers the plugin with kubelet
func (p *NUMAResourcePlugin) register() error {
	// Kubelet registration socket is always at the standard path, even on RKE2
	kubeletSocket := filepath.Join(KubeletSocketDir, KubeletSocket)

	conn, err := p.dial(kubeletSocket, 5*time.Second)
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

	_, err = client.Register(context.Background(), req)
	if err != nil {
		return fmt.Errorf("failed to register with kubelet: %w", err)
	}

	log.Printf("[NUMA-%d] Registered with kubelet as %s", p.numaNodeID, p.resourceName)
	return nil
}

// watchKubelet watches for kubelet restarts and re-registers
func (p *NUMAResourcePlugin) watchKubelet() {
	kubeletSocket := filepath.Join(KubeletSocketDir, KubeletSocket)
	backoff := time.Second
	maxBackoff := 2 * time.Minute

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
				log.Printf("[NUMA-%d] Failed to restart: %v", p.numaNodeID, err)
				backoff = min(backoff*2, maxBackoff)
				time.Sleep(backoff)
				continue
			}
			return // Exit this goroutine — Restart spawned a new one
		}

		// Check if kubelet socket exists (kubelet might have restarted)
		if _, err := os.Stat(kubeletSocket); os.IsNotExist(err) {
			log.Printf("[NUMA-%d] Kubelet socket missing, waiting...", p.numaNodeID)
			time.Sleep(time.Second)
			continue
		}

		backoff = time.Second
		time.Sleep(5 * time.Second)
	}
}

// Restart restarts the plugin (re-register with kubelet)
func (p *NUMAResourcePlugin) Restart() error {
	log.Printf("[NUMA-%d] Restarting plugin", p.numaNodeID)

	p.mu.Lock()
	// Stop the gRPC server
	if p.server != nil {
		p.server.Stop()
	}
	// Cancel old context and create a new one
	p.cancel()
	p.ctx, p.cancel = context.WithCancel(context.Background())
	p.mu.Unlock()

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

// dial creates a gRPC connection to the given socket
func (p *NUMAResourcePlugin) dial(socket string, timeout time.Duration) (*grpc.ClientConn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
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
