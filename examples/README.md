# Examples

Ready-to-use stack configurations. Copy the relevant `Pulumi.dev.yaml` to your repo root, adjust IPs, template IDs, and node names, then run `pulumi up`.

| Example | Services | Nodes | Use case |
|---|---|---|---|
| [k3s-only](k3s-only/) | K3s | 1 LB + 3 CP | Lightweight HA Kubernetes |
| [rke2-only](rke2-only/) | RKE2 | 1 LB + 3 CP | Production-grade HA Kubernetes |
| [k3s-and-rke2](k3s-and-rke2/) | K3s + RKE2 | 2 LB + 6 CP | Both clusters side by side |
| [harvester-single-node](harvester-single-node/) | Harvester | 1 node | HCI, no HA, minimal resources |
| [harvester-ha](harvester-ha/) | Harvester | 3 nodes | HCI with full HA |

## Before you start

1. Replace `youruser` with your actual OS username
2. Replace `proxmox-1`, `proxmox-2`, `proxmox-3` with your Proxmox node names
3. Replace template IDs with the IDs of your actual Proxmox templates
4. Replace IP addresses with IPs from your network range
5. Set your gateway IP
6. Run `pulumi config set password <vm-password> --secret`

See the [Template Strategy](../README.md#template-strategy) section in the main README for guidance on setting up templates.
