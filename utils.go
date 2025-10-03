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

func loadConfig(ctx *pulumi.Context) (string, string, []VM, *Services, *VMCreationConfig, error) {
	cfg := config.New(ctx, "")
	vmPassword := cfg.Require("password")
	gateway := cfg.Require("gateway")

	var vms []VM
	cfg.RequireObject("vms", &vms)

	var services Services
	cfg.RequireObject("services", &services)

	// Load VM creation config with defaults
	var vmCreationConfig VMCreationConfig
	cfg.TryObject("vmCreation", &vmCreationConfig)
	
	// Set defaults if not provided
	if vmCreationConfig.BatchSize == 0 {
		vmCreationConfig.BatchSize = 3
	}
	if vmCreationConfig.MaxRetries == 0 {
		vmCreationConfig.MaxRetries = 5
	}
	if vmCreationConfig.BatchDelay == 0 {
		vmCreationConfig.BatchDelay = 10
	}

	for i := range vms {
		if vms[i].TemplateID == 0 {
			return "", "", nil, nil, nil, fmt.Errorf("VM '%s' has no templateId set. Template ID is required and must be between 100-2147483647", vms[i].Name)
		}
		if vms[i].Name == "" {
			return "", "", nil, nil, nil, fmt.Errorf("VM at index %d has no name set", i)
		}
		if len(vms[i].IPs) == 0 {
			return "", "", nil, nil, nil, fmt.Errorf("VM '%s' has no IPs configured", vms[i].Name)
		}
		if vms[i].BootMethod == "" {
			vms[i].BootMethod = "cloud-init"
		}
		if vms[i].AuthMethod == "" {
			vms[i].AuthMethod = "ssh-key"
		}
		if vms[i].Username == "" {
			vms[i].Username = "rajeshk"
		}
		if vms[i].ProxmoxNode == "" {
			vms[i].ProxmoxNode = "proxmox-3"
		}
		if vms[i].Gateway == "" {
			vms[i].Gateway = gateway
		}
		if vms[i].IPConfig == "" {
			vms[i].IPConfig = "static"
		}
	}

	ctx.Export("vmPassword", pulumi.String(vmPassword))
	ctx.Log.Info(fmt.Sprintf("Infrastructure: Found %d VM groups to create", len(vms)), nil)

	enabledServices := getEnabledServices(&services)
	if len(enabledServices) > 0 {
		ctx.Log.Info(fmt.Sprintf("Services: Found enabled services: %v", enabledServices), nil)
	}
	return vmPassword, gateway, vms, &services, &vmCreationConfig, nil
}

func getEnabledServices(services *Services) []string {
	if services == nil {
		return []string{}
	}

	serviceRegistry := map[string]*ServiceConfig{
		"k3s":       services.K3s,
		"kubeadm":   services.Kubeadm,
		"haproxy":   services.HAProxy,
		"harvester": services.Harvester,
		"rke2":      services.RKE2,
		"talos":     services.Talos,
	}

	var enabledServices []string
	for name, config := range serviceRegistry {
		if config != nil && config.Enabled {
			enabledServices = append(enabledServices, name)
		}
	}
	return enabledServices
}

func createVMs(ctx *pulumi.Context, provider *proxmoxve.Provider, vms []VM, vmPassword string, vmCreationConfig *VMCreationConfig) (map[string][]*vm.VirtualMachine, error) {
	vmGroups := make(map[string][]*vm.VirtualMachine)

	// Track last VM created per template for dependency chaining
	lastVMPerTemplate := make(map[int64]*vm.VirtualMachine)

	// Process each VM group
	for _, vmDef := range vms {
		count := vmDef.Count
		
		// Skip if count is 0
		if count == 0 {
			ctx.Log.Info(fmt.Sprintf("Skipping VM group '%s' (count is 0)", vmDef.Name), nil)
			continue
		}

		ctx.Log.Info(fmt.Sprintf("Creating VM group '%s' (%d VMs from template %d)", 
			vmDef.Name, count, vmDef.TemplateID), nil)

		var groupVMs []*vm.VirtualMachine
		
		for i := range count {
			vmName := fmt.Sprintf("%s-%d", vmDef.Name, i)
			
			// Get dependency on last VM from same template
			var dependsOn []pulumi.Resource
			if lastVM, exists := lastVMPerTemplate[vmDef.TemplateID]; exists {
				dependsOn = []pulumi.Resource{lastVM}
				ctx.Log.Info(fmt.Sprintf("  [%d/%d] %s (waits for previous VM)", i+1, count, vmName), nil)
			} else {
				ctx.Log.Info(fmt.Sprintf("  [%d/%d] %s (first from template %d)", i+1, count, vmName, vmDef.TemplateID), nil)
			}
			
			vmInstance, err := createVMWithRetry(
				ctx,
				provider,
				i,
				vmDef,
				vmDef.ProxmoxNode,
				vmDef.Gateway,
				vmPassword,
				vmCreationConfig.MaxRetries,
				dependsOn,
			)
			
			if err != nil {
				return nil, fmt.Errorf("failed to create VM %s: %w", vmName, err)
			}

			groupVMs = append(groupVMs, vmInstance)
			
			// Update last VM for this template
			lastVMPerTemplate[vmDef.TemplateID] = vmInstance
		}
		
		vmGroups[vmDef.Name] = groupVMs
	}

	totalVMs := 0
	for _, vms := range vmGroups {
		totalVMs += len(vms)
	}
	ctx.Log.Info(fmt.Sprintf("✓ All %d VMs queued (dependencies set)", totalVMs), nil)
	return vmGroups, nil
}

func buildGlobalDependency(vmGroups map[string][]*vm.VirtualMachine, vms []VM) map[string]interface{} {
	globalDeps := make(map[string]interface{})

	for groupName, vmList := range vmGroups {
		globalDeps[groupName+"-vms"] = vmList

		var ips []string
		for _, vmDef := range vms {
			if vmDef.Name == groupName {
				for i := 0; i < len(vmList) && i < len(vmDef.IPs); i++ {
					ips = append(ips, vmDef.IPs[i])
				}
				break
			}
		}
		globalDeps[groupName+"-ips"] = ips
	}
	return globalDeps
}
