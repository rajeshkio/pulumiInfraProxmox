package main

import (
	"fmt"

	"github.com/muhlba91/pulumi-proxmoxve/sdk/v7/go/proxmoxve/vm"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// func getDefaultBaseURL(osType string) string {
// 	fmt.Println("DEBUG: from getDefaultBaseURL")
// 	switch osType {
// 	case "harvester":
// 		return "https://releases.rancher.com/harvester"
// 	case "ubuntu":
// 		return "http://archive.ubuntu.com/ubuntu"
// 	default:
// 		return ""
// 	}
// }

// func buildISOUrls(config *IPXEConfig) (string, string, string, error) {

// 	fmt.Println("DEBUG: from buildISOUrls")
// 	fmt.Printf("DEBUG: baseURL - 1%s", config.BaseURL)
// 	if config.KernelURL != "" {
// 		return config.KernelURL, config.InitrdURL, "", nil
// 	}
// 	if config.ISOURL != "" {
// 		return "", config.ISOURL, "", nil
// 	}
// 	baseURL := config.BaseURL
// 	if baseURL == "" {
// 		baseURL = getDefaultBaseURL(config.OSType)
// 	}
// 	if baseURL == "" {
// 		return "", "", "", fmt.Errorf("no base URL available for OS type: %s", config.OSType)
// 	}
// 	fmt.Printf("DEBUG: baseURL - 2%s", baseURL)
// 	fmt.Printf("DEBUG: OSType=%s, Version=%s, BaseURL=%s\n", config.OSType, config.Version, baseURL)
// 	switch config.OSType {
// 	case "harvester":
// 		return buildHarvesterURLs(baseURL, config.Version)
// 	default:
// 		return "", "", "", fmt.Errorf("unsupported OS type: %s", config.OSType)
// 	}
// }

// func buildHarvesterURLs(baseUrl, version string) (string, string, string, error) {
// 	fmt.Println("DEBUG: from buildHarvesterURLs")
// 	if version == "" {
// 		return "", "", "", fmt.Errorf("version required for Harvester")
// 	}
// 	base := fmt.Sprintf("%s/%s", baseUrl, version)
// 	kernel := fmt.Sprintf("%s/harvester-%s-vmlinuz-amd64", base, version)
// 	initrd := fmt.Sprintf("%s/harvester-%s-initrd-amd64", base, version)
// 	rootfs := fmt.Sprintf("%s/harvester-%s-rootfs-amd64.squashfs", base, version)

// 	fmt.Printf("DEBUG: kernel=%s, initrd=%s, rootfs=%s\n", kernel, initrd, rootfs)
// 	return kernel, initrd, rootfs, nil
// }
// func generateIPXEScript(template VMTemplate, vmIP string) (string, error) {
// 	fmt.Println("DEBUG: from generateIPXEScript")
// 	config := template.IPXEConfig
// 	if config == nil {
// 		return "", fmt.Errorf("iPXE config required")
// 	}
// 	kernelUrl, initrdUrl, rootfsUrl, err := buildISOUrls(config)
// 	if err != nil {
// 		return "", fmt.Errorf("failed to build URLs: %w", err)
// 	}

// 	kernelParams := strings.Join(config.KernelParams, " ")
// 	if config.ConfigUrl != "" {
// 		kernelParams += fmt.Sprintf(" harvester.install.config_url=%s", config.ConfigUrl)
// 	}
// 	if rootfsUrl != "" {
// 		kernelParams += fmt.Sprintf(" root=live:%s", rootfsUrl)
// 	}
// 	if config.AutoInstall {
// 		kernelParams += " harvester.install.automatic=true"
// 	}
// 	script := fmt.Sprintf(`#!ipxe
// 	dhcp
// 	echo Network configured: ${net0/ip}
// 	echo Downloading Harvester kernel...
// kernel %s initrd=%s %s

// echo Downloading initial ramdisk...
// initrd %s

// echo Starting Harvester installation...
// boot
// `, kernelUrl, filepath.Base(initrdUrl), kernelParams, initrdUrl)

//		return script, nil
//	}

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
