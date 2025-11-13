package main

import (
	"fmt"

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
		vmPassword, _, vms, services, vmCreationConfig, haproxyConfig, err := loadConfig(ctx)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
		//ctx.Log.Info("No VMs configured - nothing to deploy", nil)
		if len(vms) == 0 {
			ctx.Log.Info("No VMs configured - nothing to deploy", nil)
			return nil
		}

		ctx.Log.Info(fmt.Sprintf("=== PHASE 1: Infrastructure - Creating %d VM groups ===", len(vms)), nil)

		vmGroups, err := createVMs(ctx, provider, vms, vmPassword, vmCreationConfig)
		if err != nil {
			return fmt.Errorf("failed to create VMs: %s", err)
		}

		totalVMs := 0
		for groupName, vmList := range vmGroups {
			totalVMs += len(vmList)
			ctx.Export(fmt.Sprintf("%s-count", groupName), pulumi.Int(len(vmList)))

			var groupIPs []pulumi.StringInput

			for _, vmDef := range vms {
				if vmDef.Name == groupName {
					if len(vmDef.IPs) > 0 {
						for i := 0; i < len(vmList) && i < len(vmDef.IPs); i++ {
							groupIPs = append(groupIPs, pulumi.String(vmDef.IPs[i]))
						}
						ctx.Export(fmt.Sprintf("%s-ips", groupName), pulumi.StringArray(groupIPs))
					} else {
						// For DHCP-based VMs, export a note
						ctx.Export(fmt.Sprintf("%s-ip-assignment", groupName), pulumi.String("DHCP"))
						ctx.Log.Info(fmt.Sprintf("VM group %s uses DHCP - no static IPs to export", groupName), nil)
					}
					break
				}
			}
		}
		ctx.Export("totalVMsCreated", pulumi.Int(totalVMs))
		ctx.Log.Info(fmt.Sprintf("Infrastructure complete: Created %d VMs across %d groups", totalVMs, len(vmGroups)), nil)

		if services != nil {
			ctx.Log.Info("=== PHASE 2: Services - Installing software on VMs ===", nil)
			globalDeps := buildGlobalDependency(vmGroups, vms)
			globalDeps["haproxy-config"] = haproxyConfig
			err = executeServices(ctx, services, vmGroups, globalDeps, vmPassword)
			if err != nil {
				return fmt.Errorf("failed to execute services: %w", err)
			}
		} else {
			ctx.Log.Info("No services configured - VMs created without software installation", nil)
		}
		ctx.Log.Info("=== DEPLOYMENT COMPLETE ===", nil)
		ctx.Log.Info(fmt.Sprintf("Infrastructure: %d VMs created", totalVMs), nil)

		if services != nil {
			enabledServices := getEnabledServices(services)
			if len(enabledServices) > 0 {
				ctx.Log.Info(fmt.Sprintf("Services: Installed %v", enabledServices), nil)
			}
		}
		return nil
	})
}
