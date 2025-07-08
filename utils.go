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

func loadConfig(ctx *pulumi.Context) (string, string, []VMTemplate, error) {
	cfg := config.New(ctx, "")
	vmPassword := cfg.Require("password")
	gateway := cfg.Require("gateway")

	var templates []VMTemplate
	cfg.RequireObject("vm-templates", &templates)

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

	ctx.Export("vmPassword", pulumi.String(vmPassword))
	return vmPassword, gateway, templates, nil

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
