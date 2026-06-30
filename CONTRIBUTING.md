# Contributing to PulumiInfraProxmox

Thanks for your interest in contributing. This project runs real infrastructure on Proxmox VE, so contributions should be tested against an actual cluster before submitting a PR.

## How to contribute

1. Fork the repository
2. Create a feature branch: `git checkout -b feature/your-feature`
3. Make your changes
4. Verify the build passes: `go build ./...` and `go vet ./...`
5. Test against a real Proxmox environment
6. Submit a pull request

## What makes a good contribution

- A new Kubernetes distribution handler (Kubeadm, Talos, etc.)
- A new boot method
- A new configuration option with clear defaults
- A bug fix with a description of what broke and how you reproduced it
- An example configuration for a scenario not already covered in `examples/`

## Adding a new service handler

All service handlers live in `handlers.go` and follow the same signature:

```go
func myServiceHandler(ctx *pulumi.Context, serviceCtx ServiceContext) error {
    // serviceCtx.VMs        - provisioned VMs for this service
    // serviceCtx.IPs        - IP addresses in the same order as VMs
    // serviceCtx.Config     - map of config keys from Pulumi.dev.yaml
    // serviceCtx.GlobalDeps - all VM groups by name, for cross-group references
    return nil
}
```

After writing the handler:

1. Register it in the `serviceHandlers` map in `handlers.go`
2. Add the service struct field to `Services` in `types.go` if it needs its own config schema
3. Add the execution call to `executeServices` in `executers.go`
4. Add an example config to `examples/`
5. Update the Available Services table in `README.md`

## Template isolation rule

Every new service must use templates that are not shared with any existing service. This is what enables independent lifecycle management. If your service shares a template with K3s, destroying K3s breaks your service too.

## Testing checklist

- [ ] `pulumi up` deploys the service successfully
- [ ] `pulumi up` is idempotent (running it twice has no side effects)
- [ ] `pulumi destroy --target ... --target-dependents` removes only the targeted service
- [ ] Other services remain unaffected after selective destroy

## Code style

- Follow standard Go formatting: run `gofmt -w .` before committing
- Keep handlers focused: one handler per service
- Add `ctx.Log.Info(...)` messages at key steps so users can follow progress
- Return descriptive errors: `fmt.Errorf("service %s: %w", serviceName, err)`

## Reporting bugs

Use the [bug report template](https://github.com/rajeshkio/pulumiInfraProxmox/issues/new?template=bug_report.md). Include your Proxmox version, the service that failed, and the relevant log output.
