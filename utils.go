package main

import (
	"fmt"
	"os"
	"strings"

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

func loadConfig(ctx *pulumi.Context) (string, string, []VM, *Services, *VMCreationConfig, map[string]HAProxyServiceConfig, error) {
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
		// iPXE boot VMs (like Harvester) don't need a template - they boot from ISO
		if vms[i].BootMethod != "ipxe" && vms[i].TemplateID == 0 {
			return "", "", nil, nil, nil, nil, fmt.Errorf("VM '%s' has no templateId set. Template ID is required for bootMethod '%s'", vms[i].Name, vms[i].BootMethod)
		}
		if vms[i].Name == "" {
			return "", "", nil, nil, nil, nil, fmt.Errorf("VM at index %d has no name set", i)
		}

		// iPXE VMs don't need IPs (they use DHCP)
		if vms[i].BootMethod != "ipxe" && len(vms[i].IPs) == 0 {
			return "", "", nil, nil, nil, nil, fmt.Errorf("VM '%s' has no IPs configured (required for non-iPXE VMs)", vms[i].Name)
		}

		// Validate iPXE/Harvester specific configuration
		if vms[i].BootMethod == "ipxe" {
			if vms[i].IPXEConfig == nil {
				return "", "", nil, nil, nil, nil, fmt.Errorf("VM '%s' uses iPXE boot but has no ipxeConfig", vms[i].Name)
			}

			isoCount := len(vms[i].IPXEConfig.ISOFiles)
			nodeCount := vms[i].Count

			if isoCount == 0 {
				return "", "", nil, nil, nil, nil, fmt.Errorf("VM '%s' uses iPXE but has no ISO files configured", vms[i].Name)
			}

			// Critical validation: single ISO can only create single node
			if isoCount == 1 && nodeCount > 1 {
				return "", "", nil, nil, nil, nil, fmt.Errorf("VM '%s': Cannot create %d nodes with only 1 ISO. For Harvester clustering, provide 2 ISOs (create + join)",
					vms[i].Name, nodeCount)
			}

			// Warn about even node counts for Harvester
			if nodeCount == 2 {
				ctx.Log.Error(fmt.Sprintf("CRITICAL: VM '%s' has 2 nodes - this breaks etcd quorum! Use 1 or 3+ nodes", vms[i].Name), nil)
				return "", "", nil, nil, nil, nil, fmt.Errorf("VM '%s': 2-node Harvester cluster breaks etcd quorum. Use 1 node (no HA) or 3+ nodes (with HA)", vms[i].Name)
			}

			// Check for create/join pattern in ISO names
			if isoCount > 1 && nodeCount > 1 {
				hasCreate, hasJoin := false, false
				for _, iso := range vms[i].IPXEConfig.ISOFiles {
					isoLower := strings.ToLower(iso)
					if strings.Contains(isoLower, "create") || strings.Contains(isoLower, "master") || strings.Contains(isoLower, "init") {
						hasCreate = true
					}
					if strings.Contains(isoLower, "join") || strings.Contains(isoLower, "worker") || strings.Contains(isoLower, "add") {
						hasJoin = true
					}
				}

				if !hasCreate || !hasJoin {
					ctx.Log.Warn(fmt.Sprintf("VM '%s': ISO names should contain 'create'/'master' and 'join'/'worker' for automatic role detection. Will use positional logic (first=create, rest=join)", vms[i].Name), nil)
				}
			}
		}

		// Set defaults
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
	return vmPassword, gateway, vms, &services, &vmCreationConfig, nil, nil
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

	// Track last VM created per template PER NODE (to avoid NFS lock contention on same node)
	// Key format: "template-node" e.g. "9000-proxmox-2"
	lastVMPerTemplatePerNode := make(map[string]*vm.VirtualMachine)

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

			nodeName := vmDef.ProxmoxNode
			if vmDef.BootMethod == "ipxe" {
				// Distribute Harvester nodes across different Proxmox hosts
				proxmoxNodes := []string{"proxmox-1", "proxmox-2", "proxmox-3"}
				nodeName = proxmoxNodes[int(i)%len(proxmoxNodes)]
				ctx.Log.Info(fmt.Sprintf("  Harvester node %d will be on %s", i+1, nodeName), nil)
			}
			// Get dependency on last VM from same template
			var dependsOn []pulumi.Resource
			if vmDef.BootMethod == "ipxe" && i > 0 {
				// For Harvester join nodes, depend on the create node (first in group)
				if len(groupVMs) > 0 {
					dependsOn = []pulumi.Resource{groupVMs[0]}
					ctx.Log.Info(fmt.Sprintf("  [%d/%d] %s (JOIN - waits for create node)", i+1, count, vmName), nil)
				}
			} else if vmDef.BootMethod == "ipxe" && i == 0 {
				ctx.Log.Info(fmt.Sprintf("  [%d/%d] %s (CREATE - initializes cluster)", i+1, count, vmName), nil)
			} else if vmDef.TemplateID > 0 {
				// For regular VMs, use template-based dependency scoped to same node
				// This prevents NFS lock contention without creating cross-service dependencies
				templateNodeKey := fmt.Sprintf("%d-%s", vmDef.TemplateID, nodeName)
				if lastVM, exists := lastVMPerTemplatePerNode[templateNodeKey]; exists {
					dependsOn = []pulumi.Resource{lastVM}
					ctx.Log.Info(fmt.Sprintf("  [%d/%d] %s (waits for previous VM on %s)", i+1, count, vmName, nodeName), nil)
				} else {
					ctx.Log.Info(fmt.Sprintf("  [%d/%d] %s (first from template %d on %s)", i+1, count, vmName, vmDef.TemplateID, nodeName), nil)
				}
			}

			vmInstance, err := createVMWithRetry(
				ctx,
				provider,
				i,
				vmDef,
				nodeName,
				vmDef.Gateway,
				vmPassword,
				vmCreationConfig.MaxRetries,
				dependsOn,
			)

			if err != nil {
				return nil, fmt.Errorf("failed to create VM %s: %w", vmName, err)
			}

			groupVMs = append(groupVMs, vmInstance)

			if vmDef.TemplateID > 0 {
				// Update last VM for this template on this specific node
				templateNodeKey := fmt.Sprintf("%d-%s", vmDef.TemplateID, nodeName)
				lastVMPerTemplatePerNode[templateNodeKey] = vmInstance
			}
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

func validateHarvesterConfig(ctx *pulumi.Context, vms []VM) error {
	for _, vmDef := range vms {
		if vmDef.BootMethod == "ipxe" && vmDef.IPXEConfig != nil {
			isoCount := len(vmDef.IPXEConfig.ISOFiles)
			nodeCount := vmDef.Count

			if isoCount == 0 {
				return fmt.Errorf("harvester VM group '%s' requires at least one ISO file", vmDef.Name)
			}

			if isoCount == 1 && nodeCount > 1 {
				return fmt.Errorf("harvester VM group '%s': Cannot create %d nodes with only 1 ISO. For clustering, provide 2 ISOs (create + join)",
					vmDef.Name, nodeCount)
			}

			// If multiple ISOs, check we have both create and join patterns
			if isoCount > 1 && nodeCount > 1 {
				hasCreate, hasJoin := false, false
				for _, iso := range vmDef.IPXEConfig.ISOFiles {
					isoLower := strings.ToLower(iso)
					if strings.Contains(isoLower, "create") || strings.Contains(isoLower, "master") || strings.Contains(isoLower, "init") {
						hasCreate = true
					}
					if strings.Contains(isoLower, "join") || strings.Contains(isoLower, "worker") || strings.Contains(isoLower, "add") {
						hasJoin = true
					}
				}

				// Only warn, don't error - fallback to positional logic
				if !hasCreate || !hasJoin {
					ctx.Log.Warn(fmt.Sprintf("Harvester VM group '%s': ISO names should contain 'create' or 'join' for automatic detection. Will use positional logic (first=create, rest=join)", vmDef.Name), nil)
				}
			}
		}
	}
	return nil
}

func buildGlobalDependency(vmGroups map[string][]*vm.VirtualMachine, vms []VM) map[string]interface{} {
	globalDeps := make(map[string]interface{})

	for groupName, vmList := range vmGroups {
		globalDeps[groupName+"-vms"] = vmList

		for _, vmDef := range vms {
			if vmDef.Name == groupName {
				// Only store IPs if they exist
				if len(vmDef.IPs) > 0 {
					globalDeps[groupName+"-ips"] = vmDef.IPs
				} else {
					// Store empty slice or a marker for DHCP
					globalDeps[groupName+"-ips"] = []string{}
					globalDeps[groupName+"-ip-type"] = "DHCP"
				}
				break
			}
		}
	}
	return globalDeps
}
