---
name: Feature Request
about: Suggest a new service, boot method, or configuration option
title: "[Feature] "
labels: enhancement
assignees: rajeshkio
---

## What would you like to see added?

A clear description of the feature. For a new Kubernetes distribution or service, name it here.

## Why is this useful?

What problem does it solve? How many people would benefit from it?

## Proposed approach

If you have thoughts on how this should be implemented:

- New service handler in `handlers.go`?
- New boot method in `vm_creation.go`?
- New config field in `types.go`?

## Example configuration

How would this look in `Pulumi.dev.yaml`?

```yaml
proxmoxInfra:services:
  new-service:
    enabled: true
```

## Are you willing to contribute a PR?

Yes / No / Maybe
