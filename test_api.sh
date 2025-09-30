#!/bin/bash

# Source environment variables
source ./set_env.sh

# Test API connection
echo "Testing API connection..."
curl -k -H "Authorization: PVEAPIToken=$PROXMOX_VE_API_TOKEN" \
  "$PROXMOX_VE_ENDPOINT/api2/json/cluster/status" \
  --connect-timeout 10 \
  --max-time 30

echo -e "\n\nIf you see JSON output above, your API token works!"
