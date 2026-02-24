# NUMA Resource Plugin for Kubernetes

A Kubernetes device plugin that exposes NUMA topology as schedulable resources, enabling NUMA-aware pod placement through the Topology Manager.

## Overview

This plugin automatically discovers NUMA nodes on each Kubernetes node and advertises them as extended resources (`numa-align/numa-0`, `numa-align/numa-1`, etc.). When pods request these resources, the Topology Manager coordinates with the CPU Manager to ensure CPUs are allocated from the correct NUMA node.

## Motivation

At Chess.com we run several Elasticsearch clusters on Kubernetes (RKE2), with instances already pinned to specific servers and disks. These servers are multi-socket systems, and to reach maximum performance we needed a way to ensure each instance runs on the same NUMA node its disk is attached to. This plugin enables exactly that.

## Features

- **Auto-discovery**: Reads NUMA topology from `/sys/devices/system/node/online`
- **Topology hints**: Provides NUMA affinity hints to the Topology Manager
- **Configurable capacity**: Multiple pods can request the same NUMA node (default: 100 slots per node)
- **Environment injection**: Sets `NUMA_NODE` environment variable in containers
- **Kubelet restart handling**: Automatically re-registers when kubelet restarts

## Requirements

- Kubernetes 1.28+
- Go 1.24+ (for building from source). Note: `k8s.io/kubelet v0.28.0` was originally built against Go 1.20; the Go 1.24 requirement comes from indirect dependencies (`golang.org/x/net`). No compatibility issues are expected, but if you encounter odd behavior with kubelet protobuf types, this version gap is the first thing to check.
- Kubelet configured with:
  - `--topology-manager-policy=single-numa-node` (or `restricted`)
  - `--cpu-manager-policy=static`
- Pods must use:
  - Integer CPU requests (e.g., `cpu: "1"`, not `cpu: "100m"`)
  - Guaranteed QoS class (requests == limits)

### Optional: Memory Manager

By default, only CPUs are pinned to the NUMA node. To also pin memory allocations, enable the Memory Manager on each worker node:


For standard Kubernetes, add to kubelet flags:
```
--memory-manager-policy=Static
--reserved-memory=0:memory=512Mi,1:memory=512Mi
```

For RKE2, to enable both: 

```yaml
# RKE2: /etc/rancher/rke2/config.yaml
kubelet-arg:
  - "topology-manager-policy=single-numa-node"
  - "cpu-manager-policy=static"
  - "memory-manager-policy=Static"
  - "reserved-memory=0:memory=512Mi;1:memory=512Mi"  # Reserve memory per NUMA node
```

After enabling, verify memory is pinned:
```bash
# Inside container - should show only one NUMA node
cat /proc/self/status | grep Mems_allowed_list

# On host - check memory allocation
numastat -p <pid>
```

**Note**: Memory Manager requires reserving some memory on each NUMA node for system use. Adjust the `reserved-memory` values based on your node's memory capacity.

## Installation

### Build

```bash
make docker-build
make docker-push  # Pushes to registry.local:5000 - adjust as needed.
```

### Deploy

```bash
kubectl apply -f deployments/serviceaccount.yaml
kubectl apply -f deployments/configmap.yaml
kubectl apply -f deployments/daemonset.yaml
```

### Verify

```bash
# Check plugin pods
kubectl get pods -n kube-system -l app=numa-resource-plugin

# Check NUMA resources on nodes
kubectl get nodes -o custom-columns='NAME:.metadata.name,NUMA-0:.status.allocatable.numa-align/numa-0,NUMA-1:.status.allocatable.numa-align/numa-1'
```

## Usage

Request a specific NUMA node in your pod spec:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: numa-pinned-app
spec:
  containers:
    - name: app
      image: myapp:latest
      resources:
        requests:
          cpu: "2"                # Must be integer for CPU pinning
          memory: 1Gi
          numa-align/numa-0: 1    # Request NUMA node 0
        limits:
          cpu: "2"
          memory: 1Gi
          numa-align/numa-0: 1
```

The container will:
- Have `NUMA_NODE=0` environment variable set
- Have CPUs allocated from NUMA node 0 (via CPU Manager)
- Have memory allocated from NUMA node 0 (if Memory Manager is enabled)
- Be scheduled only on nodes that have NUMA node 0 available

## Configuration

Environment variables for the plugin:

| Variable | Default | Description |
|----------|---------|-------------|
| `NUMA_CAPACITY` | `100` | Number of pods that can request each NUMA node |
| `NUMA_SOCKET_DIR` | auto-detect | Override socket directory |
| `NUMA_REGISTRATION_TIMEOUT_SECONDS` | `45` | Max time to retry kubelet registration before giving up; on timeout the plugin exits so the pod restarts. Keep below the liveness probe's kill window — `(failureThreshold - 1)` × `periodSeconds`, i.e. 60s by default — minus ~5s of socket-loss detection |

## Multi-Container Pods

For NUMA pinning to work, the pod must have **Guaranteed QoS** — every container (including init containers and sidecars) must have `requests == limits` for both CPU and memory, with integer CPU values.

### Init containers must request the NUMA resource

The CPU Manager tracks init container CPUs as "reusable" since they are freed before regular containers start. During pod admission, it biases topology hints toward the NUMA node where those reusable CPUs were allocated. If the first init container lands on the wrong NUMA node (because it didn't request `numa-align`), subsequent containers that *do* require a specific NUMA node may fail with `TopologyAffinityError`.

**At minimum, the first init container must request the same `numa-align/numa-N` resource as the main workload.** Subsequent init containers will follow automatically via the reusable CPU bias.

### Sidecar containers

Regular sidecar containers (e.g., monitoring agents) don't strictly need the `numa-align` resource — they will be placed on whatever NUMA node has available CPUs. This is acceptable for lightweight workloads where cross-NUMA memory latency doesn't matter. The primary workload container should always have the request.

### Example: Pod with init container and sidecar

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: numa-pinned-app
spec:
  initContainers:
    - name: init
      image: busybox:latest
      command: ["sh", "-c", "echo initializing && sleep 5"]
      resources:
        requests:
          cpu: "1"
          memory: 128Mi
          numa-align/numa-0: 1    # Required to anchor NUMA placement
        limits:
          cpu: "1"
          memory: 128Mi
          numa-align/numa-0: 1
  containers:
    - name: app
      image: myapp:latest
      resources:
        requests:
          cpu: "4"
          memory: 8Gi
          numa-align/numa-0: 1
        limits:
          cpu: "4"
          memory: 8Gi
          numa-align/numa-0: 1
    - name: metrics
      image: metricbeat:latest
      resources:
        requests:
          cpu: "1"              # Guaranteed QoS required, numa-align optional
          memory: 256Mi
        limits:
          cpu: "1"
          memory: 256Mi
```

## How It Works

1. **Discovery**: On startup, the plugin reads `/sys/devices/system/node/online` to discover NUMA nodes
2. **Registration**: For each NUMA node, it registers a device plugin with kubelet as `numa-align/numa-N`
3. **Advertisement**: Each NUMA node advertises `capacity` devices (default: 100), each with `TopologyInfo` specifying the NUMA node ID
4. **Scheduling**: When a pod requests `numa-align/numa-N`, the scheduler finds nodes with available capacity
5. **Allocation**: The Topology Manager uses the device's `TopologyInfo` to coordinate CPU/memory allocation from the same NUMA node
6. **Injection**: The plugin sets `NUMA_NODE=N` environment variable in the allocated container

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                        Kubernetes Node                      │
│  ┌─────────────────────────────────────────────────────────┐│
│  │                      kubelet                            ││
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐   ││
│  │  │   Topology   │  │     CPU      │  │   Device     │   ││
│  │  │   Manager    │◄─┤   Manager    │◄─┤   Manager    │   ││
│  │  └──────────────┘  └──────────────┘  └──────┬───────┘   ││
│  └─────────────────────────────────────────────┼───────────┘│
│                                                │            │
│  ┌─────────────────────────────────────────────┼───────────┐│
│  │              NUMA Resource Plugin           │           ││
│  │  ┌──────────────┐  ┌──────────────┐         │           ││
│  │  │  numa-0.sock │  │  numa-1.sock │◄──────-─┘           ││
│  │  │  (gRPC)      │  │  (gRPC)      │   Registration      ││
│  │  └──────────────┘  └──────────────┘                     ││
│  └─────────────────────────────────────────────────────────┘│
│                                                             │
│  ┌─────────────┐  ┌─────────────┐                           │
│  │ NUMA Node 0 │  │ NUMA Node 1 │   /sys/devices/system/node│
│  │ CPUs: 0-3   │  │ CPUs: 4-7   │                           │
│  │ Memory: 8G  │  │ Memory: 8G  │                           │
│  └─────────────┘  └─────────────┘                           │
└─────────────────────────────────────────────────────────────┘
```

## Troubleshooting

### Plugin not registering

Check plugin logs:
```bash
kubectl logs -n kube-system -l app=numa-resource-plugin
```

Verify kubelet socket exists:
```bash
ls -la /var/lib/kubelet/device-plugins/kubelet.sock
```

### Resources not appearing on node

Check if plugin discovered NUMA nodes:
```bash
kubectl logs -n kube-system -l app=numa-resource-plugin | grep "Discovered"
```

### CPU not pinned to NUMA node

Ensure:
1. Pod uses integer CPU requests (not millicores)
2. Pod has Guaranteed QoS (requests == limits)
3. Kubelet has `--cpu-manager-policy=static`
4. Kubelet has `--topology-manager-policy=single-numa-node`

Verify CPU affinity inside container:
```bash
kubectl exec <pod> -- cat /proc/self/status | grep Cpus_allowed
```

### Pod stuck in Pending

Check if NUMA resource is available:
```bash
kubectl describe node <node> | grep -A5 "Allocatable"
```

Check pod events:
```bash
kubectl describe pod <pod> | grep -A10 "Events"
```

## Development

### Test Cluster

Create a KVM-based test cluster with NUMA topology:

```bash
cd test/infra
./create-cluster.sh
```

This creates:
- 1 registry node (Docker registry)
- 1 RKE2 server (control plane)
- 2 RKE2 workers (each with 2 NUMA nodes)

Destroy the cluster:
```bash
./destroy-cluster.sh
```

### Running Tests

```bash
make test           # Unit tests
make test-e2e       # End-to-end tests (requires cluster)
```

## License

MIT
