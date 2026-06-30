## What does this PR do?

A brief description of the change.

## Type of change

- [ ] Bug fix
- [ ] New service handler
- [ ] New configuration option
- [ ] Documentation update
- [ ] Refactor (no functional change)

## Testing

Describe how you tested this. For infrastructure changes, include the Proxmox environment and which services were deployed and destroyed.

- Proxmox VE version:
- Services tested:
- `pulumi up` result:
- `pulumi destroy` result (selective, if applicable):

## Checklist

- [ ] `go build ./...` passes
- [ ] `go vet ./...` passes
- [ ] New service uses a dedicated template (no shared templates with existing services)
- [ ] `Pulumi.dev.yaml` example included or updated if applicable
- [ ] README updated if behaviour or config keys changed
