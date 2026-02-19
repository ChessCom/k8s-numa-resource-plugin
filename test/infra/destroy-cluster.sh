#!/bin/bash
# Destroy KVM test cluster
set -e

NETWORK_NAME="numa-test"
IMAGES_DIR="/var/lib/libvirt/images"

echo "=== Destroying NUMA Test Cluster ==="

# Remove VMs
for vm in registry rke2-server rke2-worker-1 rke2-worker-2; do
    if sudo virsh dominfo "$vm" &>/dev/null; then
        echo "Removing VM: $vm"
        sudo virsh destroy "$vm" 2>/dev/null || true
        sudo virsh undefine "$vm" --remove-all-storage 2>/dev/null || true
    fi
done

# Remove cloud-init ISOs
for iso in registry rke2-server rke2-worker-1 rke2-worker-2; do
    sudo rm -f "$IMAGES_DIR/${iso}-cloudinit.iso"
done

# Optionally remove network (comment out if you want to keep it)
# if sudo virsh net-info $NETWORK_NAME &>/dev/null; then
#     echo "Removing network: $NETWORK_NAME"
#     sudo virsh net-destroy $NETWORK_NAME 2>/dev/null || true
#     sudo virsh net-undefine $NETWORK_NAME
# fi

echo "Done!"
