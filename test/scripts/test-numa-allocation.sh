#!/bin/bash
# Test NUMA resource allocation

set -e

KUBECONFIG=${KUBECONFIG:-/etc/rancher/rke2/rke2.yaml}
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MANIFEST_DIR="$SCRIPT_DIR/../manifests"

echo "=== Testing NUMA Resource Plugin ==="

# Check if plugin is running
echo ""
echo "1. Checking plugin pods..."
kubectl --kubeconfig="$KUBECONFIG" get pods -n kube-system -l app=numa-resource-plugin

# Check node resources
echo ""
echo "2. Checking node resources..."
for node in $(kubectl --kubeconfig="$KUBECONFIG" get nodes -o jsonpath='{.items[*].metadata.name}'); do
    echo "Node: $node"
    kubectl --kubeconfig="$KUBECONFIG" get node "$node" -o jsonpath='{.status.allocatable}' | jq -r 'to_entries[] | select(.key | startswith("numa-align/numa")) | "\(.key): \(.value)"' 2>/dev/null || echo "  No NUMA resources found"
    echo ""
done

# Test NUMA 0 allocation
echo "3. Testing NUMA 0 allocation..."
kubectl --kubeconfig="$KUBECONFIG" delete pod test-numa-0 --ignore-not-found
kubectl --kubeconfig="$KUBECONFIG" apply -f "$MANIFEST_DIR/test-numa-0.yaml"

echo "   Waiting for pod to be scheduled..."
kubectl --kubeconfig="$KUBECONFIG" wait --for=condition=Ready pod/test-numa-0 --timeout=60s || {
    echo "   Pod failed to start:"
    kubectl --kubeconfig="$KUBECONFIG" describe pod test-numa-0
    exit 1
}

echo "   Checking NUMA_NODE environment variable..."
numa_node=$(kubectl --kubeconfig="$KUBECONFIG" exec test-numa-0 -- sh -c 'echo $NUMA_NODE')
if [ "$numa_node" = "0" ]; then
    echo "   SUCCESS: NUMA_NODE=$numa_node"
else
    echo "   FAILED: Expected NUMA_NODE=0, got NUMA_NODE=$numa_node"
    exit 1
fi

# Test NUMA 1 allocation (if available)
echo ""
echo "4. Testing NUMA 1 allocation..."
has_numa_1=$(kubectl --kubeconfig="$KUBECONFIG" get nodes -o jsonpath='{.items[0].status.allocatable.numa-align/numa-1}' 2>/dev/null || echo "")

if [ -n "$has_numa_1" ] && [ "$has_numa_1" != "0" ]; then
    kubectl --kubeconfig="$KUBECONFIG" delete pod test-numa-1 --ignore-not-found
    kubectl --kubeconfig="$KUBECONFIG" apply -f "$MANIFEST_DIR/test-numa-1.yaml"

    echo "   Waiting for pod to be scheduled..."
    kubectl --kubeconfig="$KUBECONFIG" wait --for=condition=Ready pod/test-numa-1 --timeout=60s || {
        echo "   Pod failed to start:"
        kubectl --kubeconfig="$KUBECONFIG" describe pod test-numa-1
        exit 1
    }

    echo "   Checking NUMA_NODE environment variable..."
    numa_node=$(kubectl --kubeconfig="$KUBECONFIG" exec test-numa-1 -- sh -c 'echo $NUMA_NODE')
    if [ "$numa_node" = "1" ]; then
        echo "   SUCCESS: NUMA_NODE=$numa_node"
    else
        echo "   FAILED: Expected NUMA_NODE=1, got NUMA_NODE=$numa_node"
        exit 1
    fi
else
    echo "   SKIP: NUMA 1 not available on this node"
fi

# Cleanup
echo ""
echo "5. Cleaning up test pods..."
kubectl --kubeconfig="$KUBECONFIG" delete pod test-numa-0 test-numa-1 --ignore-not-found

echo ""
echo "=== All tests passed! ==="
