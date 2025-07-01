# PulumiInfraProxmox

A self-configuring K3s cluster deployment system using Pulumi and Go. Automatically creates and configures high-availability Kubernetes infrastructure on Proxmox VE with dynamic component discovery.

## 🚀 Quick Start

```bash
# Clone the repository
git clone https://github.com/yourusername/pulumiInfraProxmox
cd pulumiInfraProxmox

# Install dependencies
go mod download

# Configure Pulumi stack
pulumi stack init dev
pulumi config set proxmox-k3s-cluster:vm-password "your-secure-password"

# Set environment variables
export PROXMOX_API_TOKEN="your-api-token"
export SSH_PUBLIC_KEY="$(cat ~/.ssh/id_rsa.pub)"

# Deploy infrastructure
pulumi up
```

## 📁 Project Structure

```
pulumiInfraProxmox/
├── main.go                 # Main Pulumi program - infrastructure deployment logic
├── go.mod                  # Go module dependencies
├── go.sum                  # Go module checksums
├── Pulumi.yaml            # Pulumi project configuration
├── Pulumi.dev.yaml        # Development stack configuration
└── proxmox-io-error.md    # Troubleshooting guide for common Proxmox issues
```

## 🏗️ What This Deploys

- **4 VMs Total:**
  - 1x HAProxy Load Balancer (2GB RAM, 2 CPU)
  - 3x K3s Server Nodes (4GB RAM, 4 CPU each)
- **Self-Configuring Services:**
  - HAProxy automatically discovers K3s backends
  - K3s cluster forms with HA configuration
  - TLS certificates include load balancer SANs
- **Zero Manual Configuration Required**

## ⚙️ Prerequisites

### Infrastructure Requirements
- Proxmox VE server with API access
- Ubuntu 22.04 cloud template (VM ID 9000)
- Network range with static IP allocation
- Available storage for VM creation

### Local Requirements
- Go 1.19+ installed
- Pulumi CLI installed (`curl -fsSL https://get.pulumi.com | sh`)
- SSH key pair for VM authentication
- Proxmox API token with VM creation permissions

## 🔧 Configuration

### Environment Variables
```bash
export PROXMOX_API_TOKEN="PVEAPIToken=user@pam!token-id=your-token-here"
export SSH_PUBLIC_KEY="$(cat ~/.ssh/id_rsa.pub)"
```

### Pulumi Configuration
```bash
# Required settings
pulumi config set proxmox-k3s-cluster:vm-password "secure-password"
pulumi config set proxmox-k3s-cluster:proxmox-node "your-proxmox-node-name"

# Optional customization
pulumi config set proxmox-k3s-cluster:k3s-server-count 5  # Scale cluster
pulumi config set proxmox-k3s-cluster:vm-memory 8192     # Increase RAM
```

## 🚦 Usage

### Deploy Infrastructure
```bash
pulumi up
# Review the deployment plan and confirm
# ✅ Deployment completes in ~5-7 minutes
```

### Access Your Cluster
```bash
# Kubeconfig is automatically saved
export KUBECONFIG=./kubeconfig-k3s-cluster.yaml

# Verify cluster
kubectl get nodes
kubectl get pods -A
```

### Scale the Cluster
```bash
# Edit configuration to add more nodes
pulumi config set proxmox-k3s-cluster:k3s-server-count 5

# Apply changes
pulumi up
```

### Cleanup
```bash
# Destroy all resources
pulumi destroy
```

## 🔍 Outputs

After successful deployment:
```bash
Outputs:
    k3s-server-count : 3
    k3s-server-ips   : ["192.168.90.187", "192.168.90.188", "192.168.90.189"]
    kubeconfig       : [automatically saved to ./kubeconfig-k3s-cluster.yaml]
    loadbalancer-ips : ["192.168.90.190"]
    totalVMsCreated  : 4
```

## 🛠️ Troubleshooting

### Common Issues

**VM Creation Fails:**
```bash
Error: clone failed: mkdir /mnt/pve/nfs-iso/images/109: Input/output error
```
→ See [proxmox-io-error.md](./proxmox-io-error.md) for detailed NFS troubleshooting

**HAProxy Can't Reach K3s Servers:**
```bash
# Check HAProxy status
ssh ubuntu@192.168.90.190 'sudo systemctl status haproxy'

# Verify K3s API endpoints
curl -k https://192.168.90.187:6443/ping
```

**Pulumi State Issues:**
```bash
# Refresh state
pulumi refresh

# Export current state for inspection
pulumi stack export
```

### Debug Mode
```bash
# Enable verbose logging
export PULUMI_DEBUG=true
pulumi up --logtostderr -v=9 2> debug.log
```

## 🏛️ Architecture

The system uses **dynamic dependency resolution** where components automatically discover each other:

1. **VMs Created:** All VMs deployed in parallel
2. **Role Grouping:** VMs organized by their function (loadbalancer, k3s-server)
3. **Global Discovery:** Each role's IPs/resources made available to others
4. **Service Configuration:** HAProxy discovers K3s backends, K3s finds load balancer
5. **Cluster Formation:** K3s nodes join automatically with proper TLS configuration

## 🔒 Security Notes

- VMs use SSH key authentication (configurable)
- K3s API secured with TLS certificates
- Load balancer included in TLS Subject Alternative Names
- Passwords stored as Pulumi secrets
- Network traffic isolated within cluster subnet

## 📊 Performance

- **3-node cluster:** ~5m 23s deployment time
- **VM creation:** 80-90s per VM (parallel)
- **Service setup:** 20s HAProxy, 170s first K3s node
- **Memory usage:** 2GB LB + 12GB total for K3s nodes
- **Storage:** ~15GB total across all VMs

## 🤝 Contributing

1. Fork the repository
2. Create a feature branch
3. Test your changes with `pulumi preview`
4. Submit a pull request with deployment verification

## 📝 License

MIT License - see LICENSE file for details

## 🔗 Related

- [K3s Documentation](https://docs.k3s.io/)
- [Pulumi Go SDK](https://www.pulumi.com/docs/languages-sdks/go/)
- [Proxmox VE API](https://pve.proxmox.com/pve-docs/api-viewer/)
- [HAProxy Configuration](https://docs.haproxy.org/)