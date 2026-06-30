# Changelog

All notable changes to this project will be documented here.

## Unreleased

### Added
- Modernised README with badges, Mermaid architecture diagram, and corrected environment variable names
- `examples/` directory with ready-to-use configs for K3s, RKE2, K3s+RKE2, and Harvester
- GitHub Actions CI workflow (`go build` and `go vet` on push and PR)
- Issue templates for bug reports and feature requests
- PR template
- `CONTRIBUTING.md` with handler development guide
- `LICENSE` (MIT)
- `docs/` directory with banner and architecture SVGs

### Fixed
- README documented wrong environment variables (`PROXMOX_VE_PASSWORD`, `PROXMOX_VE_USERNAME`). Correct variables are `PROXMOX_VE_API_TOKEN` and `PROXMOX_VE_SSH_USERNAME`
- README listed `services.go` in project structure. The actual file is `executers.go`

## Initial Release

- Two-phase Pulumi deployment: VM provisioning then service installation
- K3s HA cluster support with HAProxy load balancer
- RKE2 HA cluster support with HAProxy load balancer
- Harvester HCI support via iPXE boot (single node and 3-node HA)
- Template-scoped sequential VM cloning to prevent NFS lock contention
- Service lifecycle isolation via dedicated templates
- Configurable VM creation: batch size, batch delay, max retries
- Kubeadm and Talos stubs (config accepted, not yet implemented)
