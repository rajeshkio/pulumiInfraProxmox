# PulumiInfraProxmox

A flexible, self-configuring multi-service deployment system using Pulumi and Go. Automatically creates and configures high-availability Kubernetes infrastructure, load balancers, and Harvester nodes on Proxmox VE with dynamic component discovery and feature flags.

## 🚀 Quick Start

```bash
# Clone the repository
git clone https://github.com/rajeshkio/pulumiInfraProxmox.git 
cd pulumiInfraProxmox

# Install dependencies
go mod download

# Configure Pulumi stack
pulumi stack init dev

# Set environment variables
export PROXMOX_VE_ENDPOINT="https://your-proxmox:8006/api2/json"
export PROXMOX_VE_USERNAME="user@pam"
export PROXMOX_VE_PASSWORD="your-password"
export PROXMOX_VE_SSH_PRIVATE_KEY="$(cat ~/.ssh/id_rsa)"
export SSH_PUBLIC_KEY="$(cat ~/.ssh/id_rsa.pub)"

# Deploy infrastructure with feature flags
pulumi up
```

## 📁 Project Structure

```
pulumiInfraProxmox/
├── main.go                 # Main Pulumi program & infrastructure logic
├── types.go               # Data structures & feature flag definitions
├── handlers.go            # Action handlers for services (HAProxy, K3s, Harvester)
├── vm_creation.go         # VM creation logic (cloud-init & iPXE boot)
├── utils.go               # Utility functions & Proxmox provider setup
├── executers.go           # Action execution engine with dependency resolution
├── go.mod                 # Go module dependencies
├── go.sum                 # Go module checksums
├── Pulumi.yaml            # Pulumi project configuration
├── Pulumi.dev.yaml        # Development stack configuration with feature flags
├── kubeconfig.yaml        # Auto-generated Kubernetes configuration
└── proxmox-io-error.md    # Troubleshooting guide for common Proxmox issues
```

## 🏗️ What This Can Deploy

### 🎛️ Feature Flags Control Everything

Choose exactly what you want to deploy:

```yaml
features:
  loadbalancer: true # HAProxy load balancer
  k3s: true # Kubernetes cluster
  harvester: false # Harvester HCI platform
```

### 📦 Available Components

- **HAProxy Load Balancer:** Ubuntu-based with automatic backend discovery
- **K3s Kubernetes Cluster:** High-availability with 3+ server nodes
- **Harvester Nodes:** iPXE-booted hyperconverged infrastructure

### 🔄 Smart Dependency Management

- HAProxy automatically discovers K3s server IPs
- K3s waits for load balancer before starting
- Kubeconfig extraction waits for K3s cluster readiness
- All dependencies resolved automatically with proper sequencing

## ⚙️ Prerequisites

### Infrastructure Requirements

- Proxmox VE server with API access
- VM Templates:
  - **Ubuntu 22.04** cloud template (VM ID 9000) for load balancer
  - **SLE Micro** template (VM ID 9001) for K3s servers
  - **Harvester iPXE ISO** for Harvester nodes
- Network range with static IP allocation capability
- NFS or local storage for VM creation

### Local Requirements

- Go 1.19+ installed
- Pulumi CLI installed (`curl -fsSL https://get.pulumi.com | sh`)
- SSH key pair for VM authentication
- Proxmox API credentials with VM creation permissions

## 🔧 Configuration

### Environment Variables

```bash
# Proxmox Connection
export PROXMOX_VE_ENDPOINT="https://your-proxmox:8006/api2/json"
export PROXMOX_VE_USERNAME="user@pam"
export PROXMOX_VE_PASSWORD="your-password"

# SSH Authentication
export PROXMOX_VE_SSH_PRIVATE_KEY="$(cat ~/.ssh/id_rsa)"
export SSH_PUBLIC_KEY="$(cat ~/.ssh/id_rsa.pub)"
```

### Pulumi Configuration (Pulumi.dev.yaml)

```yaml
config:
  # Feature flags - choose what to deploy
  proxmox-k3s-cluster:features:
    loadbalancer: true
    k3s: true
    harvester: false

  # VM password for authentication
  proxmox-k3s-cluster:password:
    secure: AAABAGl9s0KsvoAS4g84MUxnyJqWmQLGAeHMbnldANzDkde0yRVTfg==

  # Network configuration
  proxmox-k3s-cluster:gateway: 192.168.90.1

  # VM templates with roles and dependencies
  proxmox-k3s-cluster:vm-templates:
    - count: 3
      role: k3s-server
      authMethod: ssh-key # or 'password'
      ips: ["192.168.90.187", "192.168.90.188", "192.168.90.189"]
      actions:
        - type: "install-k3s-server"
          dependsOn: ["loadbalancer"]
        - type: "get-kubeconfig"
          dependsOn: ["k3s-server-install-k3s-server"]

    - count: 1
      role: loadbalancer
      authMethod: ssh-key
      ips: ["192.168.90.195"]
      actions:
        - type: "install-haproxy"

    - count: 1
      role: harvester-node
      bootMethod: "ipxe"
      ips: ["192.168.90.210"]
      actions:
        - type: "configure-ipxe-boot"
```

## 🚦 Usage Examples

### 🎯 Deploy Full Stack

```bash
# Everything enabled
pulumi config set-all --path features.loadbalancer=true \
                     --path features.k3s=true \
                     --path features.harvester=true
pulumi up
```

### 🚀 K3s Development Environment

```bash
# Just Kubernetes cluster with load balancer
pulumi config set-all --path features.loadbalancer=true \
                     --path features.k3s=true \
                     --path features.harvester=false
pulumi up
```

### 🧪 Test Load Balancer Only

```bash
# Infrastructure testing
pulumi config set-all --path features.loadbalancer=true \
                     --path features.k3s=false \
                     --path features.harvester=false
pulumi up
```

### 🔧 Harvester Evaluation

```bash
# Hyperconverged infrastructure only
pulumi config set-all --path features.loadbalancer=false \
                     --path features.k3s=false \
                     --path features.harvester=true
pulumi up
```

## 🔑 Authentication Methods

### SSH Key Authentication (Recommended)

```yaml
authMethod: ssh-key
```

- Uses SSH_PUBLIC_KEY environment variable
- More secure than passwords
- Works with cloud-init templates

### Password Authentication

```yaml
authMethod: password
```

- Uses vm-password configuration
- Requires password auth enabled in SSH config
- Useful for SLE Micro templates

## 📤 Outputs & Access

### Kubernetes Cluster Access

```bash
# Kubeconfig automatically downloaded
export KUBECONFIG=./kubeconfig.yaml

# Verify cluster health
kubectl get nodes
kubectl get pods -A

# Access via load balancer
kubectl cluster-info
```

### Deployment Outputs

```bash
# Check what was deployed
pulumi stack output

# Example outputs:
Outputs:
    k3s-server-count : 3
    k3s-server-ips   : ["192.168.90.187", "192.168.90.188", "192.168.90.189"]
    kubeconfig       : [complete kubeconfig content]
    kubeconfigPath   : "./kubeconfig.yaml"
    loadbalancer-ips : ["192.168.90.195"]
    totalVMsCreated  : 4
```

## 🔄 Staged Deployments

### Phase 1: Infrastructure

```bash
# Deploy load balancer first
pulumi config set features.loadbalancer true
pulumi config set features.k3s false
pulumi up
```

### Phase 2: Add Kubernetes

```bash
# Add K3s cluster
pulumi config set features.k3s true
pulumi up
```

### Phase 3: Add Harvester

```bash
# Add hyperconverged infrastructure
pulumi config set features.harvester true
pulumi up
```

## 🛠️ Troubleshooting

### Common Issues & Solutions

**Authentication Failures:**

```bash
# SSH key issues
error: ssh: handshake failed: ssh: unable to authenticate

# Solutions:
1. Check authMethod matches your template setup
2. Verify SSH_PUBLIC_KEY environment variable
3. Ensure templates have cloud-init configured
4. For SLE Micro: use authMethod: password
```

**Network Unreachable:**

```bash
# VM not getting IP address
error: dial tcp: connect: network is unreachable

# Solutions:
1. Fix typo: ipconfig: "static" (not "statics")
2. Verify template supports cloud-init
3. Check Proxmox network configuration
4. Increase connection timeouts for slow boot
```

**Dependency Issues:**

```bash
# Action executed before dependency ready
# Check dependency configuration:
actions:
  - type: "install-k3s-server"
    dependsOn: ["loadbalancer"]  # Role dependency
  - type: "get-kubeconfig"
    dependsOn: ["k3s-server-install-k3s-server"]  # Action dependency
```

### Debug Mode

```bash
# Enable verbose logging
export PULUMI_DEBUG=true
pulumi up --logtostderr -v=9 2> debug.log

# Check action execution order
grep "Executing action" debug.log
```

### Timeout Issues

```bash
# For slow VMs, increase timeouts in handlers.go:
pulumi.Timeouts(&pulumi.CustomTimeouts{
    Create: "20m",  # Increase from default
    Update: "20m",
    Delete: "5m",
})
```

## 🏛️ Architecture

### 🎯 Action-Based System

Each service is deployed through configurable actions:

- **install-haproxy:** Sets up load balancer with K3s backend discovery
- **install-k3s-server:** Deploys Kubernetes with HA configuration
- **get-kubeconfig:** Extracts cluster credentials with LB endpoint
- **configure-ipxe-boot:** Sets up Harvester iPXE boot configuration

### 🔗 Smart Dependency Resolution

1. **Parse Templates:** Load VM configurations with roles and actions
2. **Filter by Features:** Only include enabled components
3. **Create VMs:** Deploy infrastructure in parallel
4. **Group by Role:** Organize VMs for service discovery
5. **Execute Actions:** Run with automatic dependency resolution
6. **Global Dependencies:** Share IPs/resources between roles

### 🚀 Boot Methods

- **cloud-init:** For Ubuntu and SLE Micro templates
- **ipxe:** For Harvester bare-metal installations

## 🔒 Security Features

- **SSH Key Authentication:** Preferred over passwords
- **TLS Certificates:** K3s API secured with proper SANs
- **Load Balancer Integration:** TLS certificates include LB endpoints
- **Secret Management:** Passwords stored as Pulumi secrets
- **Network Isolation:** Components communicate within defined subnets

## 📊 Performance & Timing

### Deployment Times by Component

- **VM Creation:** 80-90s per VM (parallel execution)
- **HAProxy Setup:** ~30s after VM ready
- **K3s First Node:** ~3-4 minutes (cluster initialization)
- **K3s Additional Nodes:** ~2 minutes each (join existing)
- **Kubeconfig Extraction:** ~15s after cluster ready

### Resource Requirements

- **Load Balancer:** 2 CPU, 2GB RAM, 20GB disk
- **K3s Servers:** 10 CPU, 10GB RAM, 32GB disk each
- **Harvester Nodes:** 16 CPU, 40GB RAM, 300GB disk each

## 🤝 Contributing

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/new-component`)
3. Add your component with proper feature flag support
4. Test with different feature combinations
5. Update documentation for new features
6. Submit a pull request with deployment verification

## 🔄 Feature Roadmap

- [ ] **Multi-cloud support:** Extend beyond Proxmox
- [ ] **Helm deployments:** Automatic application installation
- [ ] **Monitoring stack:** Prometheus/Grafana integration
- [ ] **Backup automation:** Automated cluster backups
- [ ] **Rolling updates:** Zero-downtime cluster upgrades

## 📝 License

MIT License - see LICENSE file for details

## 🔗 Related Resources

- [K3s Documentation](https://docs.k3s.io/)
- [Pulumi Go SDK](https://www.pulumi.com/docs/languages-sdks/go/)
- [Proxmox VE API](https://pve.proxmox.com/pve-docs/api-viewer/)
- [HAProxy Configuration](https://docs.haproxy.org/)
- [Harvester Documentation](https://docs.harvesterhci.io/)
- [Cloud-init Documentation](https://cloud-init.readthedocs.io/)
