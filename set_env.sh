#!/bin/bash

# Proxmox Configuration - UPDATE THESE VALUES!
export PROXMOX_VE_ENDPOINT="https://192.168.90.101:8006"
export PROXMOX_VE_API_TOKEN="root@pam!api-access=YOUR_TOKEN_SECRET_HERE"

# SSH Configuration - YOUR keys for accessing Proxmox nodes
export PROXMOX_VE_SSH_USERNAME="root"
export PROXMOX_VE_SSH_PRIVATE_KEY="$(cat ~/.ssh/id_rsa)"  # YOUR private key

# VM SSH Configuration - YOUR keys for accessing created VMs
export SSH_PUBLIC_KEY="$(cat ~/.ssh/id_rsa.pub)"  # YOUR public key (gets injected into VMs)

echo "Environment variables set successfully!"
echo "PROXMOX_VE_ENDPOINT: $PROXMOX_VE_ENDPOINT"
echo "API Token configured: $(echo $PROXMOX_VE_API_TOKEN | cut -d'=' -f1)=***"
echo "SSH Username: $PROXMOX_VE_SSH_USERNAME"
echo "Private key loaded: $(echo $PROXMOX_VE_SSH_PRIVATE_KEY | wc -c) characters"
echo "Public key loaded: $(echo $SSH_PUBLIC_KEY | wc -c) characters"
