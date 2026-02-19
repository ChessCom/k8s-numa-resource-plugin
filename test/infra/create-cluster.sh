#!/bin/bash
# Create KVM-based RKE2 test cluster with NUMA topology
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
IMAGES_DIR="/var/lib/libvirt/images"
NETWORK_NAME="numa-test"
BASE_IMAGE_URL="https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img"
BASE_IMAGE="$IMAGES_DIR/ubuntu-noble-base.qcow2"
RKE2_VERSION="v1.28.15+rke2r1"

# VM Configuration
REGISTRY_IP="192.168.100.10"
SERVER_IP="192.168.100.11"
WORKER1_IP="192.168.100.20"
WORKER2_IP="192.168.100.21"

# Generate RKE2 token
RKE2_TOKEN=$(openssl rand -hex 16)

# SSH key
SSH_KEY=$(cat ~/.ssh/id_ed25519.pub 2>/dev/null || cat ~/.ssh/id_rsa.pub 2>/dev/null)
if [ -z "$SSH_KEY" ]; then
    echo "ERROR: No SSH key found. Run: ssh-keygen -t ed25519"
    exit 1
fi

echo "=== Creating NUMA Test Cluster ==="
echo "Registry: $REGISTRY_IP"
echo "Server:   $SERVER_IP"
echo "Workers:  $WORKER1_IP, $WORKER2_IP"
echo ""

# Create network if it doesn't exist
if ! sudo virsh net-info $NETWORK_NAME &>/dev/null; then
    echo "Creating network $NETWORK_NAME..."
    cat > /tmp/numa-network.xml <<EOF
<network>
  <name>$NETWORK_NAME</name>
  <forward mode='nat'/>
  <bridge name='virbr-numa' stp='on' delay='0'/>
  <ip address='192.168.100.1' netmask='255.255.255.0'>
    <dhcp>
      <range start='192.168.100.100' end='192.168.100.200'/>
      <host mac='52:54:00:00:00:10' name='registry' ip='$REGISTRY_IP'/>
      <host mac='52:54:00:00:00:11' name='rke2-server' ip='$SERVER_IP'/>
      <host mac='52:54:00:00:00:20' name='rke2-worker-1' ip='$WORKER1_IP'/>
      <host mac='52:54:00:00:00:21' name='rke2-worker-2' ip='$WORKER2_IP'/>
    </dhcp>
  </ip>
</network>
EOF
    sudo virsh net-define /tmp/numa-network.xml
    sudo virsh net-start $NETWORK_NAME
    sudo virsh net-autostart $NETWORK_NAME
fi

# Download base image if needed
if [ ! -f "$BASE_IMAGE" ]; then
    echo "Downloading Ubuntu cloud image..."
    sudo wget -q --show-progress -O "$BASE_IMAGE" "$BASE_IMAGE_URL"
fi

# Function to create a VM
create_vm() {
    local name=$1
    local memory=$2
    local vcpus=$3
    local mac=$4
    local ip=$5
    local user_data=$6
    local numa_config=${7:-""}

    local disk="$IMAGES_DIR/${name}.qcow2"
    local cloudinit_iso="$IMAGES_DIR/${name}-cloudinit.iso"

    echo "Creating VM: $name (${memory}MB RAM, ${vcpus} vCPUs)..."

    # Create disk from base image
    sudo qemu-img create -f qcow2 -F qcow2 -b "$BASE_IMAGE" "$disk" 30G

    # Create cloud-init ISO
    local meta_data=$(cat <<EOF
instance-id: $name
local-hostname: $name
EOF
)

    local tmp_dir=$(mktemp -d)
    echo "$meta_data" > "$tmp_dir/meta-data"
    echo "$user_data" > "$tmp_dir/user-data"

    # Create network-config using DHCP (IP assigned by MAC reservation in libvirt network)
    cat > "$tmp_dir/network-config" <<EOF
version: 2
ethernets:
  id0:
    match:
      macaddress: "$mac"
    dhcp4: true
EOF

    sudo genisoimage -output "$cloudinit_iso" -volid cidata -joliet -rock \
        "$tmp_dir/user-data" "$tmp_dir/meta-data" "$tmp_dir/network-config" 2>/dev/null
    rm -rf "$tmp_dir"

    # Create VM XML
    local vm_xml=$(cat <<EOF
<domain type='kvm'>
  <name>$name</name>
  <memory unit='MiB'>$memory</memory>
  <vcpu>$vcpus</vcpu>
  <os>
    <type arch='x86_64'>hvm</type>
    <boot dev='hd'/>
  </os>
  <features>
    <acpi/>
    <apic/>
  </features>
  <cpu mode='host-passthrough'>
    $numa_config
  </cpu>
  <devices>
    <disk type='file' device='disk'>
      <driver name='qemu' type='qcow2'/>
      <source file='$disk'/>
      <target dev='vda' bus='virtio'/>
    </disk>
    <disk type='file' device='cdrom'>
      <driver name='qemu' type='raw'/>
      <source file='$cloudinit_iso'/>
      <target dev='sda' bus='sata'/>
      <readonly/>
    </disk>
    <interface type='network'>
      <mac address='$mac'/>
      <source network='$NETWORK_NAME'/>
      <model type='virtio'/>
    </interface>
    <serial type='pty'>
      <target port='0'/>
    </serial>
    <console type='pty'>
      <target type='serial' port='0'/>
    </console>
    <graphics type='vnc' port='-1' autoport='yes' listen='127.0.0.1'/>
  </devices>
</domain>
EOF
)

    echo "$vm_xml" | sudo virsh define /dev/stdin
    sudo virsh start "$name"
}

# Registry cloud-init
# Test cluster only — not for production
REGISTRY_USERDATA=$(cat <<EOF
#cloud-config
users:
  - name: ubuntu
    sudo: ALL=(ALL) NOPASSWD:ALL
    shell: /bin/bash
    lock_passwd: false
    plain_text_passwd: ubuntu
    ssh_authorized_keys:
      - $SSH_KEY

package_update: true
packages:
  - docker.io

runcmd:
  - systemctl enable docker
  - systemctl start docker
  - docker run -d --restart=always --name registry -p 5000:5000 registry:2
  - echo "$REGISTRY_IP registry.local" >> /etc/hosts
EOF
)

# Server cloud-init
# Test cluster only — not for production
SERVER_USERDATA=$(cat <<EOF
#cloud-config
users:
  - name: ubuntu
    sudo: ALL=(ALL) NOPASSWD:ALL
    shell: /bin/bash
    lock_passwd: false
    plain_text_passwd: ubuntu
    ssh_authorized_keys:
      - $SSH_KEY

package_update: true
packages:
  - curl

write_files:
  - path: /etc/rancher/rke2/config.yaml
    content: |
      token: $RKE2_TOKEN
      tls-san:
        - rke2-server
        - $SERVER_IP
  - path: /etc/rancher/rke2/registries.yaml
    content: |
      mirrors:
        "registry.local:5000":
          endpoint:
            - "http://$REGISTRY_IP:5000"
      configs:
        "$REGISTRY_IP:5000":
          tls:
            insecure_skip_verify: true

runcmd:
  - echo "$REGISTRY_IP registry.local" >> /etc/hosts
  - |
    for i in \$(seq 1 60); do
      curl -s http://$REGISTRY_IP:5000/v2/ && break
      sleep 5
    done
  - curl -sfL https://get.rke2.io | INSTALL_RKE2_VERSION=$RKE2_VERSION sh -
  - systemctl enable rke2-server.service
  - systemctl start rke2-server.service
  - |
    for i in \$(seq 1 120); do
      /var/lib/rancher/rke2/bin/kubectl --kubeconfig /etc/rancher/rke2/rke2.yaml get nodes && break
      sleep 5
    done
  - chmod 644 /etc/rancher/rke2/rke2.yaml
EOF
)

# Worker cloud-init function
# Test cluster only — not for production
worker_userdata() {
    cat <<EOF
#cloud-config
users:
  - name: ubuntu
    sudo: ALL=(ALL) NOPASSWD:ALL
    shell: /bin/bash
    lock_passwd: false
    plain_text_passwd: ubuntu
    ssh_authorized_keys:
      - $SSH_KEY

package_update: true
packages:
  - curl
  - numactl

write_files:
  - path: /etc/rancher/rke2/config.yaml
    content: |
      server: https://$SERVER_IP:9345
      token: $RKE2_TOKEN
      kubelet-arg:
        - "topology-manager-policy=single-numa-node"
        - "cpu-manager-policy=static"
        - "reserved-cpus=0"
        - "kube-reserved=cpu=100m,memory=256Mi"
        - "system-reserved=cpu=100m,memory=256Mi"
  - path: /etc/rancher/rke2/registries.yaml
    content: |
      mirrors:
        "registry.local:5000":
          endpoint:
            - "http://$REGISTRY_IP:5000"
      configs:
        "$REGISTRY_IP:5000":
          tls:
            insecure_skip_verify: true

runcmd:
  - echo "$REGISTRY_IP registry.local" >> /etc/hosts
  - echo "$SERVER_IP rke2-server" >> /etc/hosts
  - numactl --hardware || true
  - |
    for i in \$(seq 1 60); do
      curl -s http://$REGISTRY_IP:5000/v2/ && break
      sleep 5
    done
  - |
    for i in \$(seq 1 120); do
      curl -sk https://$SERVER_IP:9345/ping && break
      sleep 5
    done
  - curl -sfL https://get.rke2.io | INSTALL_RKE2_VERSION=$RKE2_VERSION INSTALL_RKE2_TYPE=agent sh -
  - systemctl enable rke2-agent.service
  - systemctl start rke2-agent.service
EOF
}

# NUMA configuration for workers (2 nodes, 2 vCPUs each, 4GB each)
NUMA_CONFIG='<numa>
      <cell id="0" cpus="0-1" memory="4194304" unit="KiB"/>
      <cell id="1" cpus="2-3" memory="4194304" unit="KiB"/>
    </numa>'

# Clean up any existing VMs
for vm in registry rke2-server rke2-worker-1 rke2-worker-2; do
    if sudo virsh dominfo "$vm" &>/dev/null; then
        echo "Removing existing VM: $vm"
        sudo virsh destroy "$vm" 2>/dev/null || true
        sudo virsh undefine "$vm" --remove-all-storage 2>/dev/null || true
    fi
done

# Create VMs
create_vm "registry" 2048 2 "52:54:00:00:00:10" "$REGISTRY_IP" "$REGISTRY_USERDATA"
sleep 5
create_vm "rke2-server" 4096 2 "52:54:00:00:00:11" "$SERVER_IP" "$SERVER_USERDATA"
sleep 5
create_vm "rke2-worker-1" 8192 4 "52:54:00:00:00:20" "$WORKER1_IP" "$(worker_userdata)" "$NUMA_CONFIG"
sleep 5
create_vm "rke2-worker-2" 8192 4 "52:54:00:00:00:21" "$WORKER2_IP" "$(worker_userdata)" "$NUMA_CONFIG"

echo ""
echo "=== VMs Created ==="
sudo virsh list --all | grep -E "(registry|rke2)"
echo ""
echo "Waiting for cluster to be ready..."
echo "This takes 5-10 minutes for RKE2 to install and start."
echo ""
echo "To check progress:"
echo "  ssh ubuntu@$SERVER_IP 'sudo journalctl -u rke2-server -f'"
echo ""
echo "Once ready, get kubeconfig:"
echo "  scp ubuntu@$SERVER_IP:/etc/rancher/rke2/rke2.yaml ./kubeconfig"
echo "  sed -i 's/127.0.0.1/$SERVER_IP/g' kubeconfig"
echo "  export KUBECONFIG=\$(pwd)/kubeconfig"
echo ""
echo "RKE2 Token: $RKE2_TOKEN"

# Save cluster info
cat > "$SCRIPT_DIR/cluster-info.env" <<EOF
REGISTRY_IP=$REGISTRY_IP
SERVER_IP=$SERVER_IP
WORKER1_IP=$WORKER1_IP
WORKER2_IP=$WORKER2_IP
RKE2_TOKEN=$RKE2_TOKEN
EOF

echo "Cluster info saved to: $SCRIPT_DIR/cluster-info.env"
