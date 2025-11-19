# PulumiInfraProxmox

A flexible, service-oriented infrastructure deployment system for Proxmox VE using Pulumi and Go. Deploy high-availability Kubernetes clusters (K3s, RKE2, Kubeadm), Harvester HCI, and HAProxy load balancers with intelligent service discovery and automatic configuration.

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

# Deploy infrastructure
pulumi up
```

## 📁 Project Structure
```
pulumiInfraProxmox/
├── main.go                 # Main Pulumi program & orchestration
├── types.go               # Data structures & service definitions
├── handlers.go            # Service handlers (K3s, RKE2, Harvester, HAProxy)
├── vm_creation.go         # VM creation (cloud-init & iPXE boot)
├── utils.go               # Configuration loading & utilities
├── services.go            # Service execution engine
├── go.mod                 # Go module dependencies
├── Pulumi.yaml            # Pulumi project configuration
└── Pulumi.dev.yaml        # Stack configuration with services
```

## 🏗️ Architecture

### Two-Phase Deployment

1. **Infrastructure Phase**: Creates VMs in batches with proper dependencies
2. **Services Phase**: Installs and configures software on VMs

### Smart Service Discovery

Services automatically discover their target VMs through the configuration. No manual IP management needed - services find their VMs by target names.

## 📦 Available Services

### Kubernetes Distributions
- **K3s**: Lightweight Kubernetes with HA support
- **RKE2**: Production-grade Kubernetes 
- **Kubeadm**: Native Kubernetes deployment

### Infrastructure Services
- **HAProxy**: Multi-backend load balancer with dynamic configuration
- **Harvester**: Hyperconverged infrastructure platform (iPXE boot)

## ⚙️ Configuration

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
  # Core settings
  proxmox-k3s-cluster:password:
    secure: AAABAG...
  proxmox-k3s-cluster:gateway: "192.168.90.1"
  
  # VM Creation Configuration
  proxmox-k3s-cluster:vmCreation:
    batchSize: 3        # VMs created in parallel
    maxRetries: 5       # Retry failed VM creation
    batchDelay: 10      # Seconds between batches
  
  # VM Definitions
  proxmox-k3s-cluster:vms:
    # Load Balancer
    - name: "load-balancer"
      count: 1
      templateId: 9000
      memory: 2048
      cpu: 2
      diskSize: 20
      ips: ["192.168.90.195"]
      proxmoxNode: "proxmox-2"
      
    # K3s Cluster
    - name: "k3s-servers"
      count: 3
      templateId: 9001
      memory: 10240
      cpu: 10
      diskSize: 32
      ips: ["192.168.90.187", "192.168.90.188", "192.168.90.189"]
      proxmoxNode: "proxmox-2"
      
    # RKE2 Cluster
    - name: "rke2-servers"
      count: 3
      templateId: 9001
      memory: 10240
      cpu: 10
      diskSize: 64
      ips: ["192.168.90.200", "192.168.90.201", "192.168.90.202"]
      proxmoxNode: "proxmox-2"
      
    # Worker Nodes
    - name: "workers"
      count: 2
      templateId: 9000
      memory: 10240
      cpu: 10
      diskSize: 32
      ips: ["192.168.90.210", "192.168.90.211"]
      proxmoxNode: "proxmox-2"
      
    # Harvester HCI Nodes (iPXE boot - no template)
    - name: "harvester-nodes"
      count: 3
      bootMethod: "ipxe"
      ipxeConfig:
        isoFiles:
          - "harvester-create-v1.4.3.iso"
          - "harvester-join-v1.4.3.iso"
      memory: 40000
      cpu: 12
      diskSize: 600
      # No IPs - uses DHCP
      # Automatically distributed across proxmox-1, proxmox-2, proxmox-3
  
  # Service Definitions
  proxmox-k3s-cluster:services:
    k3s:
      enabled: true
      targets: ["k3s-servers"]
      config:
        version: "v1.29.0+k3s1"
        
    rke2:
      enabled: true
      targets: ["rke2-servers", "workers"]
      config:
        version: "v1.29.0+rke2r1"
        serverIPs: ["192.168.90.200"]
        
    kubeadm:
      enabled: false
      targets: ["kubeadm-servers"]
      
    haproxy:
      enabled: true
      targets: ["load-balancer"]
      config:
        stats:
          enabled: true
          port: 8404
          user: "admin"
          password: "admin123"
        backends:
          - name: "k3s-api"
            port: 6443
            targets: ["k3s-servers"]
          - name: "rke2-api"
            port: 9345
            targets: ["rke2-servers"]
            
    harvester:
      enabled: true
      targets: ["harvester-nodes"]
      config:
        version: "v1.4.3"
        boot-server-url: "http://192.168.90.18"
```

## 🔄 Boot Methods

### Cloud-Init (Default)
- Used for Ubuntu, SLE Micro, and other cloud-ready templates
- Requires template with cloud-init installed
- Supports SSH keys and network configuration

### iPXE Boot
- Used for Harvester bare-metal installations
- Boots from custom iPXE ISOs
- Pattern-based ISO selection (create/join)
- DHCP network configuration only

## 🎯 Deployment Examples

### Full Stack Deployment
```yaml
# Enable all services
services:
  k3s:
    enabled: true
  rke2:
    enabled: true
  haproxy:
    enabled: true
  harvester:
    enabled: true
```

### K3s with Load Balancer
```yaml
# Just K3s cluster
services:
  k3s:
    enabled: true
  haproxy:
    enabled: true
  rke2:
    enabled: false
  harvester:
    enabled: false
```

### Harvester HCI Only
```yaml
# Hyperconverged infrastructure
services:
  harvester:
    enabled: true
  k3s:
    enabled: false
```

## 🔑 Key Features

### Intelligent VM Creation
- **Batch processing** with configurable parallelism
- **Automatic retry** on failures with disk resize detection
- **Template dependency** management
- **Cross-Proxmox node** distribution for Harvester

### Service-Oriented Architecture
- Services discover VMs by target names
- Automatic IP aggregation from multiple VM groups
- HAProxy dynamically configures backends
- Proper sequencing with service dependencies

### Harvester iPXE Support
- **Pattern-based ISO selection**: Automatically detects create vs join ISOs
- **Node distribution**: Spreads nodes across Proxmox hosts
- **Sequential deployment**: Join nodes wait for create node
- **Etcd quorum validation**: Prevents 2-node clusters

## 📤 Outputs
```bash
# Check deployment results
pulumi stack output

# Example outputs:
Outputs:
  harvester-node-count: 3
  harvester-ip-assignment: "DHCP"
  k3s-servers-count: 3
  k3s-servers-ips: ["192.168.90.187", "192.168.90.188", "192.168.90.189"]
  load-balancer-count: 1
  load-balancer-ips: ["192.168.90.195"]
  rke2-servers-count: 3
  rke2-servers-ips: ["192.168.90.200", "192.168.90.201", "192.168.90.202"]
  totalVMsCreated: 12
```

## 🛠️ Troubleshooting

### Harvester Deployment Issues
```bash
# Check node status
ssh rancher@<node-dhcp-ip>
sudo systemctl status rke2-server
sudo journalctl -u rke2-server -n 50

# Verify VIP is active
ip addr show | grep <vip-address>

# Check cluster formation
kubectl get nodes
```

### VM Creation Failures
```bash
# Enable debug logging
export PULUMI_DEBUG=true
pulumi up --logtostderr -v=9 2> debug.log

# Check for disk resize errors
grep "disk resize" debug.log

# Verify template IDs
grep "templateId" Pulumi.dev.yaml
```

### Service Installation Issues
```bash
# Check service logs
pulumi logs --follow

# Verify service targets
grep -A5 "services:" Pulumi.dev.yaml

# Check IP assignments
pulumi stack output | grep ips
```

## 📊 Resource Requirements

| Component | CPU | Memory | Disk | Network |
|-----------|-----|--------|------|---------|
| Load Balancer | 2 | 2GB | 20GB | Static IP |
| K3s Server | 10 | 10GB | 32GB | Static IP |
| RKE2 Server | 10 | 10GB | 64GB | Static IP |
| Worker Node | 10 | 10GB | 32GB | Static IP |
| Harvester Node | 12 | 40GB | 600GB | DHCP |

## 🚀 iPXE Setup for Harvester

### Create Boot ISOs
```bash
# Helper function for building iPXE ISOs
function ipxemake {
    # ... see repository for full implementation
    # Builds harvester-create-v1.4.3.iso and harvester-join-v1.4.3.iso
}

# Build ISOs
ipxemake v1.4.3
```

### Required Files on Boot Server
```
/var/www/pxe/versions/v1.4.3/
├── harvester-create-config.yaml    # Create node configuration
├── harvester-join-2-config.yaml    # Join node 2 configuration
├── harvester-join-3-config.yaml    # Join node 3 configuration
├── harvester-create-boot.ipxe      # Create node boot script
├── harvester-join-2-boot.ipxe      # Join node 2 boot script
└── harvester-join-3-boot.ipxe      # Join node 3 boot script
```

## 🔒 Security Considerations

- SSH key authentication preferred over passwords
- Pulumi secrets for sensitive data
- TLS certificates for K3s/RKE2 APIs
- HAProxy stats interface protected
- Network isolation between services

## 🤝 Contributing

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/new-service`)
3. Add service handler in `handlers.go`
4. Update types in `types.go`
5. Test with different configurations
6. Submit pull request with examples

## 📝 License

MIT License - see LICENSE file for details

## 🔗 Resources

- [Pulumi Documentation](https://www.pulumi.com/docs/)
- [Proxmox VE API](https://pve.proxmox.com/wiki/Proxmox_VE_API)
- [K3s Documentation](https://docs.k3s.io/)
- [RKE2 Documentation](https://docs.rke2.io/)
- [Harvester Documentation](https://docs.harvesterhci.io/)
- [HAProxy Documentation](http://www.haproxy.org/)
