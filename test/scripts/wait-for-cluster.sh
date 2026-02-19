#!/bin/bash
# Wait for RKE2 cluster to be ready

set -e

TIMEOUT=${TIMEOUT:-300}
KUBECONFIG=${KUBECONFIG:-/etc/rancher/rke2/rke2.yaml}

echo "Waiting for cluster to be ready (timeout: ${TIMEOUT}s)..."

start_time=$(date +%s)

while true; do
    current_time=$(date +%s)
    elapsed=$((current_time - start_time))

    if [ $elapsed -ge $TIMEOUT ]; then
        echo "Timeout waiting for cluster"
        exit 1
    fi

    # Check if kubectl can reach the API server
    if kubectl --kubeconfig="$KUBECONFIG" get nodes &>/dev/null; then
        # Check if all nodes are Ready
        not_ready=$(kubectl --kubeconfig="$KUBECONFIG" get nodes --no-headers 2>/dev/null | grep -v " Ready " | wc -l)
        if [ "$not_ready" -eq 0 ]; then
            echo "All nodes are ready!"
            kubectl --kubeconfig="$KUBECONFIG" get nodes
            exit 0
        fi
        echo "Waiting for nodes to be ready... ($not_ready not ready, ${elapsed}s elapsed)"
    else
        echo "Waiting for API server... (${elapsed}s elapsed)"
    fi

    sleep 5
done
