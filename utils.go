package main

import (
	"fmt"
	"os"

	"github.com/muhlba91/pulumi-proxmoxve/sdk/v7/go/proxmoxve"
	"github.com/muhlba91/pulumi-proxmoxve/sdk/v7/go/proxmoxve/vm"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

func checkRequiredEnvVars() error {
	required := []string{
		"SSH_PUBLIC_KEY",
		"PROXMOX_VE_SSH_USERNAME",
		"PROXMOX_VE_ENDPOINT",
		"PROXMOX_VE_API_TOKEN",
		"PROXMOX_VE_SSH_PRIVATE_KEY",
	}

	var missingEnvVars []string
	for _, envVar := range required {
		if os.Getenv(envVar) == "" {
			missingEnvVars = append(missingEnvVars, envVar)
		}
	}
	if len(missingEnvVars) > 0 {
		return fmt.Errorf("missing required environment variables: %v", missingEnvVars)
	}
	return nil
}

func setupProxmoxProvider(ctx *pulumi.Context) (*proxmoxve.Provider, error) {
	provider, err := proxmoxve.NewProvider(ctx, "proxmox-provider", &proxmoxve.ProviderArgs{
		Ssh: &proxmoxve.ProviderSshArgs{
			PrivateKey: pulumi.String(os.Getenv("PROXMOX_VE_SSH_PRIVATE_KEY")),
			Username:   pulumi.String(os.Getenv("PROXMOX_VE_SSH_USERNAME")),
		},
		Insecure: pulumi.Bool(true), // for self signed certificate
	})
	if err != nil {
		return nil, err
	}
	return provider, nil
}

func loadConfig(ctx *pulumi.Context) (string, string, []VMTemplate, []VMGroup, Features, error) {
	cfg := config.New(ctx, "")
	vmPassword := cfg.Require("password")
	gateway := cfg.Require("gateway")

	var templates []VMTemplate
	cfg.RequireObject("vm-templates", &templates)

	var vmGroups []VMGroup
	cfg.TryObject("vm-groups", &vmGroups)

	var features Features
	cfg.RequireObject("features", &features)

	for i := range templates {
		if templates[i].BootMethod == "" {
			templates[i].BootMethod = "cloud-init"
		}
		if templates[i].AuthMethod == "" {
			templates[i].AuthMethod = "password"
		}
		if templates[i].Username == "" {
			templates[i].Username = "rajeshk"
		}
		if templates[i].ProxmoxNode == "" {
			templates[i].ProxmoxNode = "proxmox-3"
		}
	}

	for i := range vmGroups {
		if vmGroups[i].BootMethod == "" {
			vmGroups[i].BootMethod = "cloud-init"
		}
		if vmGroups[i].AuthMethod == "" {
			vmGroups[i].AuthMethod = "ssh-key"
		}
		if vmGroups[i].Username == "" {
			vmGroups[i].Username = "rajeshk"
		}
		if vmGroups[i].ProxmoxNode == "" {
			vmGroups[i].ProxmoxNode = "proxmox-3"
		}
		if vmGroups[i].Gateway == "" {
			vmGroups[i].Gateway = gateway
		}
	}

	ctx.Export("vmPassword", pulumi.String(vmPassword))
	ctx.Log.Info(fmt.Sprintf("Features - Loadbalancer: %v, K3s: %v, Harvester: %v",
		features.Loadbalancer, features.K3s, features.Harvester), nil)

	if len(vmGroups) > 0 {
		ctx.Log.Info(fmt.Sprintf("Found %d VM groups for bare VM creation", len(vmGroups)), nil)
	}
	return vmPassword, gateway, templates, vmGroups, features, nil

}

func groupVMsByRole(allVMs []*vm.VirtualMachine, templates []VMTemplate) (map[string]RoleGroup, error) {
	roleGroups := make(map[string]RoleGroup) // map with key string and value of type rolegroup
	vmIndex := 0
	expectedVMCount := 0

	for _, template := range templates {
		count := template.Count
		if count == 0 {
			count = 1
		}
		if len(template.IPs) < int(count) {
			return nil, fmt.Errorf("template '%s' role '%s' needs %d IPs but only has %d: %v", template.Name, template.Role, count, len(template.IPs), template.IPs)
		}
		expectedVMCount += int(count)
	}
	if expectedVMCount != len(allVMs) {
		return nil, fmt.Errorf("VM count mismatch: expected %d VMs but got %d", expectedVMCount, len(allVMs))
	}

	// Now safely build the groups
	for _, template := range templates {
		count := template.Count
		if count == 0 {
			count = 1
		}
		for i := range count {
			if _, exists := roleGroups[template.Role]; !exists {
				roleGroups[template.Role] = RoleGroup{}
			}
			group := roleGroups[template.Role]
			group.VMs = append(group.VMs, allVMs[vmIndex])
			group.IPs = append(group.IPs, template.IPs[i])
			roleGroups[template.Role] = group
			vmIndex++
		}
	}
	return roleGroups, nil
}

func buildGlobalDependency(roleGroups map[string]RoleGroup) map[string]interface{} {
	globalDeps := make(map[string]interface{})

	for roleName, group := range roleGroups {
		globalDeps[roleName+"-ips"] = group.IPs
		globalDeps[roleName+"-vms"] = group.VMs
	}
	return globalDeps
}

func createBareVMs(ctx *pulumi.Context, provider *proxmoxve.Provider, vmGroups []VMGroup, vmPassword string) (map[string][]*vm.VirtualMachine, error) {
	bareVMs := make(map[string][]*vm.VirtualMachine)

	for _, group := range vmGroups {
		ctx.Log.Info(fmt.Sprintf("Creating bare VM group '%s' with %d VMs", group.Name, group.Count), nil)

		var groupVMs []*vm.VirtualMachine
		count := group.Count
		if count == 0 {
			count = 1
		}

		for i := range count {
			// Convert VMGroup to VMTemplate for compatibility with existing createVMFromTemplate
			template := VMTemplate{
				Name:        group.Name,
				VMName:      group.Name,
				ID:          group.TemplateID,
				Memory:      group.Memory,
				CPU:         group.CPU,
				DiskSize:    group.DiskSize,
				IPs:         group.IPs,
				Gateway:     group.Gateway,
				Username:    group.Username,
				AuthMethod:  group.AuthMethod,
				ProxmoxNode: group.ProxmoxNode,
				BootMethod:  group.BootMethod,
				IPXEConfig:  group.IPXEConfig,
				IPConfig:    "static", // Default for bare VMs
			}

			vm, err := createVMFromTemplate(ctx, provider, i, template, group.ProxmoxNode, group.Gateway, vmPassword)
			if err != nil {
				return nil, fmt.Errorf("failed to create VM %s-%d: %w", group.Name, i, err)
			}

			groupVMs = append(groupVMs, vm)
			ctx.Log.Info(fmt.Sprintf("Created bare VM: %s-%d", group.Name, i), nil)

			if i < count-1 {
				ctx.Log.Info("Waiting 30 seconds before creating next VM to avoid storage locks...", nil)
			}
		}

		bareVMs[group.Name] = groupVMs
	}

	return bareVMs, nil
}
