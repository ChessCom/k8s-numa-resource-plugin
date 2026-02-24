package main

import (
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/chesscom/k8s-numa-resource-plugin/pkg/config"
	"github.com/chesscom/k8s-numa-resource-plugin/pkg/plugin"
	"github.com/chesscom/k8s-numa-resource-plugin/pkg/topology"
)

func main() {
	log.Println("Starting NUMA Resource Plugin")

	// Load configuration. config reports only what the operator asked for; the
	// plugin package owns the registration-timeout default, so resolve it here
	// rather than keeping a second copy of the value in config.
	cfg := config.Load()
	registrationTimeout := cfg.RegistrationTimeout
	if registrationTimeout <= 0 {
		registrationTimeout = plugin.DefaultRegistrationTimeout
	}
	log.Printf("Configuration: socketDir=%s, capacity=%d, registrationTimeout=%s",
		cfg.SocketDir, cfg.Capacity, registrationTimeout)

	// Discover NUMA topology
	topo, err := topology.DiscoverNUMATopology()
	if err != nil {
		log.Fatalf("Failed to discover NUMA topology: %v", err)
	}

	log.Printf("Discovered %d NUMA node(s)", len(topo.Nodes))
	for _, node := range topo.Nodes {
		log.Printf("  NUMA Node %d", node.ID)
	}

	// Create every plugin instance up front (before starting any of them) so
	// the onFatal callback below can safely range over a fully-built slice
	// regardless of how fast any one plugin registers.
	plugins := make([]*plugin.NUMAResourcePlugin, 0, len(topo.Nodes))
	for _, node := range topo.Nodes {
		p := plugin.NewPlugin(node.ID, cfg.SocketDir, cfg.Capacity)
		p.SetRegistrationTimeout(registrationTimeout)
		plugins = append(plugins, p)
	}

	// A NUMA node that never manages to register - whether during initial
	// startup or after a later kubelet restart - is worse than no plugin at
	// all: callers would see a subset of NUMA resources and silently miss the
	// rest. So any unrecoverable registration failure, at any point in the
	// process lifetime, stops every plugin and exits fatally: the DaemonSet
	// restarts the pod and everything retries from a clean slate.
	var fatalOnce sync.Once
	fatal := func(reason string, err error) {
		fatalOnce.Do(func() {
			log.Printf("%s: %v", reason, err)
			for _, p := range plugins {
				p.Stop()
			}
			log.Fatalf("NUMA resource plugin exiting due to unrecoverable registration failure: %v", err)
		})
		// The goroutine that won the Once never gets here - log.Fatalf exits
		// the process. Everyone else must not get here either: sync.Once makes
		// losing calls return immediately, and a fatal() that returns would let
		// its caller carry on as if the failure had been recoverable (the
		// startup loop below would count a dead plugin as started). Park
		// instead, and let the winner take the process down.
		select {}
	}

	// Start each plugin instance for its NUMA node
	errCh := make(chan error, len(plugins))
	for _, p := range plugins {
		p.SetOnFatal(func(err error) { fatal("Plugin failed to re-register after kubelet restart", err) })
		go func(p *plugin.NUMAResourcePlugin) {
			errCh <- p.Start()
		}(p)
	}

	// Wait for all plugins to report back on their initial registration. Any
	// error is fatal and never returns, so reaching the end of this loop means
	// every plugin registered.
	for range plugins {
		if err := <-errCh; err != nil {
			fatal("Plugin failed to register", err)
		}
	}
	log.Printf("All %d plugins started successfully", len(plugins))

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down...")
	for _, p := range plugins {
		p.Stop()
	}
	log.Println("Shutdown complete")
}
