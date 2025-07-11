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
		vmPassword, gateway, templates, vmGroups, features, err := loadConfig(ctx)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
		enabledTemplates := filterEnabledTemplates(ctx, templates, features)
		if len(enabledTemplates) == 0 {
			ctx.Log.Info("No templates enabled - nothing to deploy", nil)
			return nil
		}

		var allRoleVMs []*vm.VirtualMachine
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
				allRoleVMs = append(allRoleVMs, vm)
				ctx.Log.Info(fmt.Sprintf("Created VM: %s", fmt.Sprintf("%s-%d", template.VMName, i)), nil)
			}
		}

		roleGroups, err := groupVMsByRole(allRoleVMs, enabledTemplates)
		if err != nil {
			return fmt.Errorf("cannot group VM by role")
		}
		globalDeps := buildGlobalDependency(roleGroups)

		for roleName, group := range roleGroups {
			ctx.Log.Info(fmt.Sprintf("Role '%s': %d with VM with IPs %v", roleName, len(group.VMs), group.IPs), nil)
		}

		ctx.Export("totalVMsCreated", pulumi.Int(len(allRoleVMs)))
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

		var totalBareVMs int
		if len(vmGroups) > 0 {
			ctx.Log.Info(fmt.Sprintf("Creating %d bare VM groups", len(vmGroups)), nil)
			bareVMs, err := createBareVMs(ctx, provider, vmGroups, vmPassword)
			if err != nil {
				return fmt.Errorf("failed to create bare VMs: %w", err)
			}
			for groupName, vms := range bareVMs {
				totalBareVMs += len(vms)
				ctx.Export(fmt.Sprintf("bare-%s-count", groupName), pulumi.Int(len(vms)))
				var groupIPs []pulumi.StringInput
				group := vmGroups[0]
				for _, g := range vmGroups {
					if g.Name == groupName {
						group = g
						break
					}
				}
				for i := 0; i < len(vms); i++ {
					if i < len(group.IPs) {
						groupIPs = append(groupIPs, pulumi.String(group.IPs[i]))
					}
				}
				ctx.Export(fmt.Sprintf("bare-%s-ips", groupName), pulumi.StringArray(groupIPs))
			}
			ctx.Export("totalBareVMsCreated", pulumi.Int(totalBareVMs))
			ctx.Log.Info(fmt.Sprintf("Successfully created %d bare VMs across %d groups", totalBareVMs, len(bareVMs)), nil)
		} else {
			ctx.Log.Info("No bare VM groups configured", nil)
		}
		ctx.Export("totalVMsCreated", pulumi.Int(len(allRoleVMs)+totalBareVMs))
		ctx.Log.Info(fmt.Sprintf("Deployment complete - Role VMs: %d, Bare VMs: %d", len(allRoleVMs), totalBareVMs), nil)
		return nil
	})
}
