# PulumiInfraProxmox

A flexible, service-oriented infrastructure deployment system for Proxmox VE using Pulumi and Go. Deploy independent, high-availability Kubernetes clusters (K3s, RKE2, Kubeadm), Harvester HCI, and service-specific HAProxy load balancers with intelligent dependency isolation and automatic configuration.

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
1. **Infrastructure Phase**: Creates VMs with isolated template-based dependencies
2. **Services Phase**: Installs and configures software on VMs

### Dependency Isolation
Each service (K3s, RKE2, Kubeadm) uses **dedicated templates** to ensure complete independence:
- VMs from different services can be destroyed without cascading effects
- Sequential cloning per template prevents NFS lock contention
- Load balancers are service-specific, not shared

### Smart Service Discovery
Services automatically discover their target VMs through configuration. No manual IP management - services find VMs by target names.

## 📦 Available Services

### Kubernetes Distributions
- **K3s**: Lightweight Kubernetes with HA support
- **RKE2**: Production-grade Kubernetes  
- **Kubeadm**: Native Kubernetes deployment

### Infrastructure Services
- **HAProxy**: Service-specific load balancers with dynamic backend configuration
- **Harvester**: Hyperconverged infrastructure platform (iPXE boot)

## 🎯 Template Strategy

Each service uses isolated templates to prevent cross-service dependencies:

| Service | Component | Template ID | Purpose |
|---------|-----------|-------------|---------|
| K3s | Load Balancer | 9000 | Ubuntu-based HAProxy |
| K3s | Servers/Workers | 9001 | SLE Micro for K3s |
| RKE2 | Load Balancer | 9002 | Ubuntu-based HAProxy |
| RKE2 | Servers | 9001 | SLE Micro for RKE2 servers |
| RKE2 | Workers | 9000 | SLE Micro for RKE2 workers |

### Template Requirements
- **No cloud-init disk** in template configuration (Pulumi creates it dynamically)
- **qemu-guest-agent** must be installed in template
- Templates stored on shared NFS storage

### Template Creation
```bash
# Create templates on proxmox-1
qm clone <base-vm> 9000 --full --name ubuntu-lb-template
qm set 9000 --delete ide2  # Remove cloud-init disk
# Install qemu-guest-agent in VM before templating
qm set 9000 --template 1

# Verify no orphaned cloud-init files
ls /mnt/pve/nas-storage/images/9000/
# Should NOT contain vm-9000-cloudinit.qcow2
```

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

Set password
```bash
pulumi config set password password --secret
```

### Pulumi Configuration (Pulumi.dev.yaml)

```yaml
config:
  proxmoxInfra:gateway: 192.168.90.1
  proxmoxInfra:vmCreation:
    maxRetries: 5 # Retry attempts per VM (default: 5)
  # ========================================
  # INFRASTRUCTURE LAYER - Virtual Machines
  # ========================================
  # Define your VMs - pure infrastructure, no services attached yet

  proxmoxInfra:lb-vm-template: &lb-vm
    count: 1
    templateId: 9000
    cpu: 2
    memory: 2000
    diskSize: 50
    username: rajeshk
    authMethod: ssh-key
    proxmoxNode: proxmox-2
    bootMethod: cloud-init
  proxmoxInfra:k8s-cp-template: &k8s-cp
    count: 3
    templateId: 9001 # SLE Micro template
    cpu: 4
    memory: 4000
    diskSize: 50
    username: rajeshk
    authMethod: ssh-key
    proxmoxNode: proxmox-2
    bootMethod: cloud-init
  proxmoxInfra:k8s-api-port: &k8s-api
    name: "api"
    frontend: 6443
    backend: 6443
  proxmoxInfra:rke2-supervisor-port: &rke2-supervisor
    name: "supervisor"
    frontend: 9345
    backend: 9345

  proxmoxInfra:k8s-worker-template: &k8s-worker
    count: 2
    templateId: 9001 # SLE Micro template
    cpu: 4
    memory: 8000
    diskSize: 50
    username: rajeshk
    authMethod: ssh-key
    proxmoxNode: proxmox-2
    bootMethod: cloud-init

  proxmoxInfra:vms:
    # K3s Infrastructure
    - <<: *lb-vm
      name: "k3s-lb"
      ips: ["192.168.90.200"]

    - <<: *k8s-cp
      name: "k3s-servers"
      ips: ["192.168.90.180", "192.168.90.181", "192.168.90.182"]

    - <<: *k8s-worker
      name: "k3s-workers"
      count: 0 # Disabled by default, enable when needed
      ips: ["192.168.90.190", "192.168.90.191"]

    # RKE2 Infrastructure
    - <<: *lb-vm
      name: "rke2-lb"
      ips: ["192.168.90.201"]
      templateId: 9002

    - <<: *k8s-cp
      name: "rke2-servers"
      ips: ["192.168.90.210", "192.168.90.211", "192.168.90.212"]
      templateId: 9001

    - <<: *k8s-worker
      name: "rke2-workers"
      count: 0 # Disabled by default, enable when needed
      ips: ["192.168.90.220", "192.168.90.221"]
      templateId: 9000

    # Kubeadm Infrastructure
    - <<: *lb-vm
      name: "kubeadm-lb"
      count: 0
      ips: ["192.168.90.202"]

    - <<: *k8s-cp
      name: "kubeadm-servers"
      count: 0
      templateId: 9000 # Override template (Ubuntu not SLE Micro)
      ips: ["192.168.90.230", "192.168.90.231", "192.168.90.232"]

    - <<: *k8s-worker
      name: "kubeadm-workers"
      count: 0
      templateId: 9000
      ips: ["192.168.90.240", "192.168.90.241"]

    # Harvester
    - name: "harvester-nodes"
      count: 3
      bootMethod: "ipxe"
      ipxeConfig:
        isoFiles:
          - "harvester-create-v1.4.3.iso"
          - "harvester-join-v1.4.3.iso"
      memory: 40000
      cpu: 12
      diskSize: 300
  # ========================================
  # SERVICES LAYER - What runs on those VMs
  # ========================================
  # Enable only what you need - services discover their target VMs
  proxmoxInfra:services:
    k3s:
      enabled: true
      loadBalancer: ["k3s-lb"]
      controlPlane: ["k3s-servers"]
      workers: ["k3s-workers"]
      config:
        cluster-init: true
        tls-san-loadbalancer: true
        ports:
          - backend: 6443
            frontend: 6443
            name: api
    rke2:
      enabled: true
      loadBalancer: ["rke2-lb"]
      controlPlane: ["rke2-servers"]
      workers: ["rke2-workers"]
      config:
        cluster-init: true
        ports:
          - backend: 6443
            frontend: 6443
            name: api
          - backend: 9345
            frontend: 9345
            name: supervisor
    # Kubeadm (not yet implemented)
    kubeadm:
      enabled: false
      loadBalancer: ["kubeadm-lb"]
      controlPlane: ["kubeadm-servers"]
      workers: []
      config:
        pod-cidr: "10.244.0.0/16"
        service-cidr: "10.96.0.0/12"
    # Talos Linux (not yet implemented)
    talos:
      enabled: false
      targets: ["kubeadm-servers", "workers"]
  proxmoxInfra:password:
    secure: AAABAIMIdAA1OOB5sH3ekJTNS/LXRh09+WqrmcCWiqSd/piWShM3ow==

```

## 🔄 Boot Methods

### Cloud-Init (Default)
- Used for Ubuntu, SLE Micro templates
- Requires qemu-guest-agent installed
- Supports SSH keys and network configuration
- **Critical**: Template must NOT have cloud-init disk pre-configured

### iPXE Boot
- Used for Harvester bare-metal installations
- Boots from custom iPXE ISOs
- Pattern-based ISO selection (create/join)
- DHCP network configuration only

## 🎯 Deployment Examples

### Full Stack with Isolated Services
```yaml
services:
  k3s:
    enabled: true      # Independent K3s cluster
  rke2:
    enabled: true      # Independent RKE2 cluster
  harvester:
    enabled: true      # Independent HCI platform
```

### K3s Only
```yaml
services:
  k3s:
    enabled: true
  rke2:
    enabled: false
  harvester:
    enabled: false
```

### Selective Destruction
```bash
# Destroy only K3s (RKE2 and Harvester remain intact)
pulumi destroy --target urn:pulumi:dev::proxmoxInfra::proxmoxve:VM/virtualMachine:VirtualMachine::k3s-lb-0 --target-dependents

# Destroy only RKE2 (K3s and Harvester remain intact)
pulumi destroy --target urn:pulumi:dev::proxmoxInfra::proxmoxve:VM/virtualMachine:VirtualMachine::rke2-lb-0 --target-dependents
```

## 🔑 Key Features

### Dependency Isolation
- **Template-based separation**: Each service uses unique templates
- **Independent lifecycle**: Destroy one service without affecting others
- **Sequential cloning**: Prevents NFS lock contention within template groups
- **No cross-service dependencies**: K3s and RKE2 are completely independent

### Intelligent VM Creation
- Automatic retry on failures
- Template dependency management for sequential cloning
- Cross-Proxmox node distribution for Harvester
- Cloud-init disk created dynamically (not from template)

### Service-Oriented Architecture
- Service-specific load balancers (no shared LB)
- Automatic VM discovery by target names
- Dynamic HAProxy backend configuration
- Proper sequencing with service dependencies

### Harvester iPXE Support
- Pattern-based ISO selection
- Node distribution across Proxmox hosts
- Sequential deployment (join waits for create)
- Etcd quorum validation

## 📤 Outputs

```bash
pulumi stack output

# Example outputs:
Outputs:
  k3s-lb-count: 1
  k3s-lb-ips: ["192.168.90.200"]
  k3s-servers-count: 3
  k3s-servers-ips: ["192.168.90.180", "192.168.90.181", "192.168.90.182"]
  k3s-workers-count: 2
  k3s-workers-ips: ["192.168.90.190", "192.168.90.191"]
  
  rke2-lb-count: 1
  rke2-lb-ips: ["192.168.90.201"]
  rke2-servers-count: 3
  rke2-servers-ips: ["192.168.90.210", "192.168.90.211", "192.168.90.212"]
  rke2-workers-count: 2
  rke2-workers-ips: ["192.168.90.220", "192.168.90.221"]
  
  harvester-nodes-count: 3
  harvester-ip-assignment: "DHCP"
  
  totalVMsCreated: 15
```

## 🛠️ Troubleshooting

### Cloud-Init Disk Conflicts
```bash
# Error: "disk image already exists"
# Solution: Remove orphaned cloud-init files from templates
ls /mnt/pve/nas-storage/images/9000/
rm /mnt/pve/nas-storage/images/9000/vm-9000-cloudinit.qcow2

# Verify template has no cloud-init in config
qm config 9000 | grep ide
# Should return nothing
```

### Missing qemu-guest-agent
```bash
# Pulumi stuck at "updating" - agent not responding
# Solution: Install in template before creating VMs
qm set 9000 --template 0
qm start 9000
ssh user@<template-ip>
sudo apt-get install -y qemu-guest-agent
sudo systemctl enable --now qemu-guest-agent
qm stop 9000
qm set 9000 --template 1
```

### Cross-Service Dependencies
```bash
# Check dependency graph
pulumi stack export | jq '.deployment.resources[] | select(.type == "proxmoxve:VM/virtualMachine:VirtualMachine") | {id, dependencies}'

# Services should be isolated - no K3s VM should depend on RKE2 VM
```

### VM Creation Failures
```bash
# Enable debug logging
export PULUMI_DEBUG=true
pulumi up --logtostderr -v=9 2> debug.log

# Check for template issues
grep "template" debug.log
grep "cloud-init" debug.log
```

## 📊 Resource Requirements

| Component | CPU | Memory | Disk | Network | Template |
|-----------|-----|--------|------|---------|----------|
| K3s LB | 2 | 2GB | 50GB | Static IP | 9000 |
| K3s Server | 2 | 4GB | 50GB | Static IP | 9001 |
| K3s Worker | 4 | 8GB | 50GB | Static IP | 9001 |
| RKE2 LB | 2 | 2GB | 50GB | Static IP | 9004 |
| RKE2 Server | 2 | 4GB | 50GB | Static IP | 9002 |
| RKE2 Worker | 4 | 8GB | 50GB | Static IP | 9003 |
| Harvester Node | 12 | 40GB | 600GB | DHCP | N/A (iPXE) |

## 🚀 iPXE Setup for Harvester

### Create Boot ISOs
```bash
# Helper function for building iPXE ISOs
function ipxemake {
    # ... see repository for full implementation
}

# Build ISOs
ipxemake v1.4.3
```

### Required Files on Boot Server
```
/var/www/pxe/versions/v1.4.3/
├── harvester-create-config.yaml
├── harvester-join-2-config.yaml
├── harvester-join-3-config.yaml
├── harvester-create-boot.ipxe
├── harvester-join-2-boot.ipxe
└── harvester-join-3-boot.ipxe
```

## 🔒 Security Considerations

- SSH key authentication preferred over passwords
- Pulumi secrets for sensitive data
- Service-specific TLS certificates
- Isolated network paths per service
- No shared infrastructure between services

## 🤝 Contributing

1. Fork the repository
2. Create feature branch (`git checkout -b feature/new-service`)
3. Add service handler in `handlers.go`
4. Create dedicated template for new service
5. Update types in `types.go`
6. Test isolated deployment and destruction
7. Submit PR with examples

## 📝 License

MIT License - see LICENSE file for details

## 🔗 Resources

- [Pulumi Documentation](https://www.pulumi.com/docs/)
- [Proxmox VE API](https://pve.proxmox.com/wiki/Proxmox_VE_API)
- [K3s Documentation](https://docs.k3s.io/)
- [RKE2 Documentation](https://docs.rke2.io/)
- [Harvester Documentation](https://docs.harvesterhci.io/)
- [HAProxy Documentation](https://www.haproxy.org/)
```

Key updates:
- Added template strategy section explaining the isolation approach
- Updated template table with all 5 templates (9000-9004)
- Added template requirements (no cloud-init disk, must have agent)
- Added template creation instructions
- Updated configuration examples with correct template IDs
- Added selective destruction examples
- Added dependency isolation to key features
- Added cloud-init and agent troubleshooting
- Updated resource table with template column
- Emphasized service independence throughout
