---
name: Bug Report
about: Report a problem with VM creation, service installation, or configuration
title: "[Bug] "
labels: bug
assignees: rajeshkio
---

## What happened?

A clear description of the bug. Include the exact error message if available.

## What did you expect to happen?

## Steps to reproduce

1.
2.
3.

## Environment

| Field | Value |
|---|---|
| Pulumi version | `pulumi version` |
| Go version | `go version` |
| Proxmox VE version | |
| Service affected | K3s / RKE2 / Harvester / Other |
| Boot method | cloud-init / iPXE |

## Relevant logs

Run with debug logging and paste the relevant output:

```bash
PULUMI_DEBUG=true pulumi up --logtostderr -v=3 2>&1 | tail -50
```

```
paste logs here
```

## Pulumi.dev.yaml (redact sensitive values)

```yaml
paste config here
```
