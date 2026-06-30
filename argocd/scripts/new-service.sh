#!/bin/bash
set -e

SERVICE_NAME=$1

if [ -z "$SERVICE_NAME" ]; then
  echo "Usage: ./new-service.sh <service-name>"
  exit 1
fi

DEST="services/${SERVICE_NAME}"

if [ -d "$DEST" ]; then
  echo "Error: services/${SERVICE_NAME} already exists"
  exit 1
fi

mkdir -p "$DEST"
cp -r templates/golden-path-service/* "$DEST"/

# Patch the service name into values.yaml
yq -i ".serviceName = \"${SERVICE_NAME}\"" "$DEST/values.yaml"

echo "Service scaffolded at $DEST"
echo "Next steps:"
echo "  1. Edit $DEST/values.yaml to set your image"
echo "  2. git add, commit, push"
echo "  3. ArgoCD will auto-deploy it"
