#!/bin/bash
# Test NUMA resource allocation

set -e

KUBECONFIG=${KUBECONFIG:-/etc/rancher/rke2/rke2.yaml}
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MANIFEST_DIR="$SCRIPT_DIR/../manifests"

# Count of tests skipped for lack of hardware. Set REQUIRE_ALL_TESTS=1 (e.g. in
# CI, on a known multi-NUMA node) to make any skip a hard failure.
skipped=0

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

# Test NUMA 1 allocation (if any node advertises it)
echo ""
echo "4. Testing NUMA 1 allocation..."
# Check every node, not just .items[0]: the first node in the list is typically
# the single-NUMA control plane, so keying off it skipped this test even when a
# multi-NUMA worker was present - and the run still reported success.
numa_1_nodes=$(kubectl --kubeconfig="$KUBECONFIG" get nodes -o json 2>/dev/null \
    | jq -r '[.items[] | select((.status.allocatable["numa-align/numa-1"] // "0") != "0")] | length' 2>/dev/null || echo 0)
numa_1_nodes=${numa_1_nodes:-0}

if [ "$numa_1_nodes" -gt 0 ]; then
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
    echo "   SKIP: no node advertises numa-align/numa-1"
    skipped=$((skipped + 1))
fi

# Cleanup
echo ""
echo "5. Cleaning up test pods..."
kubectl --kubeconfig="$KUBECONFIG" delete pod test-numa-0 test-numa-1 --ignore-not-found

echo ""
if [ "$skipped" -gt 0 ]; then
    # Never report a clean pass over a skipped test: a run that silently skips
    # the multi-NUMA path looks identical to one that actually exercised it.
    echo "=== Tests passed, but $skipped test(s) were SKIPPED - see above ==="
    if [ -n "$REQUIRE_ALL_TESTS" ]; then
        echo "    REQUIRE_ALL_TESTS is set - treating skips as failure"
        exit 1
    fi
else
    echo "=== All tests passed! ==="
fi

exit 0
