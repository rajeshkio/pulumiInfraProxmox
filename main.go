package main

import (
	"fmt"

	"github.com/muhlba91/pulumi-proxmoxve/sdk/v7/go/proxmoxve/vm"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {

		if err := checkRequiredEnvVars(); err != nil {
			return err
		}

		provider, err := setupProxmoxProvider(ctx)
		if err != nil {
			return fmt.Errorf("failed to setup Proxmox provider: %w", err)
		}
		vmPassword, gateway, templates, features, err := loadConfig(ctx)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
		enabledTemplates := filterEnabledTemplates(ctx, templates, features)
		if len(enabledTemplates) == 0 {
			ctx.Log.Info("No templates enabled - nothing to deploy", nil)
			return nil
		}

		var allVMs []*vm.VirtualMachine
		for _, template := range enabledTemplates {
			count := template.Count
			if count == 0 {
				count = 1
			}
			proxmoxNode := template.ProxmoxNode
			if proxmoxNode == "" {
				proxmoxNode = "proxmox-3" // Use global default
			}
			for i := range count {
				vm, err := createVMFromTemplate(ctx, provider, i, template, proxmoxNode, gateway, vmPassword)
				if err != nil {
					return fmt.Errorf("cannot create VM %s: %w", fmt.Sprintf("%s-%d", template.VMName, i), err)
				}
				allVMs = append(allVMs, vm)
				ctx.Log.Info(fmt.Sprintf("Created VM: %s", fmt.Sprintf("%s-%d", template.VMName, i)), nil)
			}
		}

		roleGroups, err := groupVMsByRole(allVMs, enabledTemplates)
		if err != nil {
			return fmt.Errorf("cannot group VM by role")
		}
		globalDeps := buildGlobalDependency(roleGroups)

		for roleName, group := range roleGroups {
			ctx.Log.Info(fmt.Sprintf("Role '%s': %d with VM with IPs %v", roleName, len(group.VMs), group.IPs), nil)
		}

		ctx.Export("totalVMsCreated", pulumi.Int(len(allVMs)))
		for roleName, group := range roleGroups {
			ctx.Export(fmt.Sprintf("%s-count", roleName), pulumi.Int(len(group.VMs)))
			ctx.Export(fmt.Sprintf("%s-ips", roleName), pulumi.StringArray(
				func() []pulumi.StringInput {
					result := make([]pulumi.StringInput, len(group.IPs))
					for i, ip := range group.IPs {
						result[i] = pulumi.String(ip)
					}
					return result
				}(),
			))
		}

		err = executeActions(ctx, enabledTemplates, roleGroups, globalDeps, vmPassword)
		if err != nil {
			return fmt.Errorf("failed to execute actions %s", err)
		}
		return nil
	})
}
