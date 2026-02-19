package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/chesscom/k8s-numa-resource-plugin/pkg/config"
	"github.com/chesscom/k8s-numa-resource-plugin/pkg/plugin"
	"github.com/chesscom/k8s-numa-resource-plugin/pkg/topology"
)

func main() {
	log.Println("Starting NUMA Resource Plugin")

	// Load configuration
	cfg := config.Load()
	log.Printf("Configuration: socketDir=%s, capacity=%d",
		cfg.SocketDir, cfg.Capacity)

	// Discover NUMA topology
	topo, err := topology.DiscoverNUMATopology()
	if err != nil {
		log.Fatalf("Failed to discover NUMA topology: %v", err)
	}

	log.Printf("Discovered %d NUMA node(s)", len(topo.Nodes))
	for _, node := range topo.Nodes {
		log.Printf("  NUMA Node %d", node.ID)
	}

	// Create and start one plugin instance per NUMA node
	plugins := make([]*plugin.NUMAResourcePlugin, 0, len(topo.Nodes))
	errCh := make(chan error, len(topo.Nodes))
	for _, node := range topo.Nodes {
		p := plugin.NewPlugin(node.ID, cfg.SocketDir, cfg.Capacity)
		plugins = append(plugins, p)

		go func(p *plugin.NUMAResourcePlugin) {
			errCh <- p.Start()
		}(p)
	}

	// Wait for all plugins to report back
	var started int
	for range topo.Nodes {
		if err := <-errCh; err != nil {
			log.Printf("Plugin failed to start: %v", err)
		} else {
			started++
		}
	}
	if started == 0 {
		log.Fatalf("All plugins failed to start, exiting")
	}
	log.Printf("%d/%d plugins started successfully", started, len(topo.Nodes))

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
