package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/muhlba91/pulumi-proxmoxve/sdk/v7/go/proxmoxve"
	"github.com/muhlba91/pulumi-proxmoxve/sdk/v7/go/proxmoxve/vm"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func isDiskResizeError(err error) bool {
	if err == nil {
		return false
	}

	return strings.Contains(err.Error(), "disk resize failure")
}

func createVMWithRetry(ctx *pulumi.Context, provider *proxmoxve.Provider, vmIndex int64, vmDef VM, nodeName, gateway, password string, maxRetries int, dependsOn []pulumi.Resource) (*vm.VirtualMachine, error) {
	vmName := fmt.Sprintf("%s-%d", vmDef.Name, vmIndex)
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		ctx.Log.Info(fmt.Sprintf("Creating VM %s (attempt %d/%d)", vmName, attempt, maxRetries), nil)

		vmInstance, err := createVM(ctx, provider, vmIndex, vmDef, nodeName, gateway, password, dependsOn)

		if err == nil {
			if attempt > 1 {
				ctx.Log.Info(fmt.Sprintf("VM %s created after %d attempts", vmName, attempt), nil)
			} else {
				ctx.Log.Info(fmt.Sprintf("VM %s created", vmName), nil)
			}
			return vmInstance, nil
		}

		lastErr = err

		// Disk resize error - don't retry
		if isDiskResizeError(err) {
			ctx.Log.Error(fmt.Sprintf("Disk size mismatch for %s", vmName), nil)
			return nil, fmt.Errorf("disk size configuration error: %w", err)
		}

		// For any other error, retry with fixed delay
		if attempt < maxRetries {
			waitTime := 10 * time.Second
			ctx.Log.Warn(fmt.Sprintf("VM creation failed: %v", err), nil)
			ctx.Log.Info(fmt.Sprintf("   Waiting %v before retry %d/%d...", waitTime, attempt+1, maxRetries), nil)
			time.Sleep(waitTime)
			continue
		}
	}

	return nil, fmt.Errorf("failed to create VM %s after %d attempts: %w", vmName, maxRetries, lastErr)
}

func createVM(ctx *pulumi.Context, provider *proxmoxve.Provider, vmIndex int64, vmDef VM, nodeName, gateway, password string, dependsOn []pulumi.Resource) (*vm.VirtualMachine, error) {

	ctx.Log.Info(fmt.Sprintf("Creating VM %s on node %s (method: %s)",
		fmt.Sprintf("%s-%d", vmDef.Name, vmIndex), nodeName, vmDef.BootMethod), nil)

	switch vmDef.BootMethod {
	case "ipxe":
		return createIPXEVM(ctx, provider, vmIndex, vmDef, nodeName, gateway, password, dependsOn)
	case "cloud-init":
		return createCloudInitVM(ctx, provider, vmIndex, vmDef, nodeName, gateway, password, dependsOn)
	default:
		return nil, fmt.Errorf("unsupported boot method: %s", vmDef.BootMethod)
	}
}

func createCloudInitVM(ctx *pulumi.Context, provider *proxmoxve.Provider, vmIndex int64, vmDef VM, nodeName, gateway, password string, dependsOn []pulumi.Resource) (*vm.VirtualMachine, error) {

	var userAccount *vm.VirtualMachineInitializationUserAccountArgs
	if vmDef.AuthMethod == "ssh-key" {
		sshKey := strings.TrimSpace(os.Getenv("SSH_PUBLIC_KEY"))
		userAccount = &vm.VirtualMachineInitializationUserAccountArgs{
			Username: pulumi.String(vmDef.Username),
			Keys: pulumi.StringArray{
				pulumi.String(sshKey),
			},
		}
	} else {
		// For SLE VMs: Use password authentication
		userAccount = &vm.VirtualMachineInitializationUserAccountArgs{
			Username: pulumi.String(vmDef.Username),
			Password: pulumi.String(password),
		}
	}

	var ipConfig *vm.VirtualMachineInitializationIpConfigArray
	if vmDef.IPConfig == "static" {
		ctx.Export(fmt.Sprintf("vmIndex:%d", vmIndex), nil)
		ctx.Export(fmt.Sprintf("len of template.IPs:%d", len(vmDef.IPs)), nil)
		if vmIndex >= int64(len(vmDef.IPs)) {
			return nil, fmt.Errorf("not enough IPs provided for VM %d", vmIndex)
		}
		ipConfig = &vm.VirtualMachineInitializationIpConfigArray{
			&vm.VirtualMachineInitializationIpConfigArgs{
				Ipv4: vm.VirtualMachineInitializationIpConfigIpv4Args{
					Address: pulumi.String(vmDef.IPs[vmIndex] + "/24"),
					Gateway: pulumi.String(gateway),
				},
			},
		}
	} else {
		ipConfig = nil
	}
	vmName := fmt.Sprintf("%s-%d", vmDef.Name, vmIndex)

	// Build resource options with dependencies
	opts := []pulumi.ResourceOption{
		pulumi.Provider(provider),
		pulumi.DeleteBeforeReplace(true),
		pulumi.IgnoreChanges([]string{"clone", "disks"}),
	}

	// Add dependencies if provided
	if len(dependsOn) > 0 {
		opts = append(opts, pulumi.DependsOn(dependsOn))
	}

	vmInstance, err := vm.NewVirtualMachine(ctx, vmDef.Name+fmt.Sprintf("-%d", vmIndex), &vm.VirtualMachineArgs{
		Name:     pulumi.String(vmName),
		NodeName: pulumi.String(nodeName),
		Memory: &vm.VirtualMachineMemoryArgs{
			Dedicated: pulumi.Int(vmDef.Memory),
		},
		Cpu: &vm.VirtualMachineCpuArgs{
			Cores: pulumi.Int(vmDef.CPU),
			Type:  pulumi.String("x86-64-v2-AES"),
		},
		Clone: &vm.VirtualMachineCloneArgs{
			NodeName: pulumi.String("proxmox-1"), // hardcoding this for now as I have all the templates on proxmox-1. TODO somehow automate this as well.
			VmId:     pulumi.Int(vmDef.TemplateID),
			Full:     pulumi.Bool(true),
			Retries:  pulumi.Int(3), // Retry clone operation up to 3 times
		},
		Cdrom: &vm.VirtualMachineCdromArgs{
			FileId: pulumi.String("none"),
		},
		Disks: &vm.VirtualMachineDiskArray{
			&vm.VirtualMachineDiskArgs{
				Interface:  pulumi.String("scsi0"),
				Size:       pulumi.Int(vmDef.DiskSize), // Match your template's disk size
				FileFormat: pulumi.String("raw"),
			},
		},
		NetworkDevices: &vm.VirtualMachineNetworkDeviceArray{
			&vm.VirtualMachineNetworkDeviceArgs{
				Bridge:   pulumi.String("vmbr0"),
				Model:    pulumi.String("virtio"),
				Firewall: pulumi.Bool(true),
			},
		},
		Initialization: &vm.VirtualMachineInitializationArgs{
			DatastoreId: pulumi.String("nas-storage"),
			UserAccount: userAccount,
			Dns: &vm.VirtualMachineInitializationDnsArgs{
				Domain: pulumi.String("local"),
				Servers: pulumi.StringArray{
					pulumi.String("192.168.90.1"),
					pulumi.String("8.8.8.8"),
				},
			},
			IpConfigs: ipConfig,
		},
		Started: pulumi.Bool(true),
		OnBoot:  pulumi.Bool(false),
	}, opts...)
	if err != nil {
		return nil, err
	}
	return vmInstance, nil
}

func createIPXEVM(ctx *pulumi.Context, provider *proxmoxve.Provider, vmIndex int64, vmDef VM, nodeName, gateway, password string, dependsOn []pulumi.Resource) (*vm.VirtualMachine, error) {
	if vmDef.IPXEConfig == nil {
		return nil, fmt.Errorf("iPXE boot method requires ipxeconfig")
	}

	if len(vmDef.IPXEConfig.ISOFiles) == 0 {
		return nil, fmt.Errorf("iPXE boot requires at least one ISO file in isoFiles")
	}

	if len(vmDef.IPXEConfig.ISOFiles) == 1 && vmDef.Count > 1 {
		return nil, fmt.Errorf("cannot create %d nodes with only 1 ISO file. For a cluster, provide at least 2 ISOs (create + join)", vmDef.Count)
	}

	vmName := fmt.Sprintf("%s-%d", vmDef.Name, vmIndex)

	var createISO, joinISO string
	var createISOs, joinISOs []string

	for _, iso := range vmDef.IPXEConfig.ISOFiles {
		isoLower := strings.ToLower(iso)
		if strings.Contains(isoLower, "create") || strings.Contains(isoLower, "master") || strings.Contains(isoLower, "init") {
			createISOs = append(createISOs, iso)
		} else if strings.Contains(isoLower, "join") || strings.Contains(isoLower, "worker") || strings.Contains(isoLower, "add") {
			joinISOs = append(joinISOs, iso)
		}
	}

	if len(createISOs) > 0 {
		createISO = createISOs[0]
	}
	if len(joinISOs) > 0 {
		joinISO = joinISOs[0]
	}

	var isoFileName string
	var nodeRole string

	if len(vmDef.IPXEConfig.ISOFiles) == 1 {
		// Single ISO - only valid for single node
		isoFileName = vmDef.IPXEConfig.ISOFiles[0]
		nodeRole = "SINGLE"
		ctx.Log.Info(fmt.Sprintf("Single node deployment using ISO: %s", isoFileName), nil)
	} else {
		// Multiple ISOs - cluster deployment
		if vmIndex == 0 {
			// First node uses create ISO
			if createISO != "" {
				isoFileName = createISO
				nodeRole = "CREATE"
			} else {
				return nil, fmt.Errorf("no create ISO found (looked for patterns: create, master, init)")
			}
		} else {
			// Other nodes use join ISO
			if joinISO != "" {
				isoFileName = joinISO
				nodeRole = "JOIN"
			} else {
				return nil, fmt.Errorf("no join ISO found (looked for patterns: join, worker, add)")
			}
		}
		ctx.Log.Info(fmt.Sprintf("Node %d (%s) using ISO: %s", vmIndex+1, nodeRole, isoFileName), nil)
	}
	ctx.Log.Info(fmt.Sprintf("VM %s using ISO: %s", vmName, isoFileName), nil)

	// Build resource options with dependencies
	opts := []pulumi.ResourceOption{
		pulumi.Provider(provider),
		pulumi.DeleteBeforeReplace(true),
	}

	if vmIndex == 0 {
		// Create node needs time to initialize cluster
		opts = append(opts, pulumi.Timeouts(&pulumi.CustomTimeouts{
			Create: "30m",
			Update: "30m",
			Delete: "10m",
		}))
	} else {
		// Join nodes need more time as they wait for create node
		opts = append(opts, pulumi.Timeouts(&pulumi.CustomTimeouts{
			Create: "45m",
			Update: "45m",
			Delete: "10m",
		}))
	}

	// Add dependencies if provided
	if len(dependsOn) > 0 {
		opts = append(opts, pulumi.DependsOn(dependsOn))
	}

	vmInstance, err := vm.NewVirtualMachine(ctx, vmDef.Name+fmt.Sprintf("-%d", vmIndex), &vm.VirtualMachineArgs{
		Name:     pulumi.String(vmName),
		NodeName: pulumi.String(nodeName),
		Agent: &vm.VirtualMachineAgentArgs{
			Enabled: pulumi.Bool(false), // Disable to prevent ide3 cdrom from being added
		},
		Memory: &vm.VirtualMachineMemoryArgs{
			Dedicated: pulumi.Int(vmDef.Memory),
		},
		Cpu: &vm.VirtualMachineCpuArgs{
			Cores: pulumi.Int(vmDef.CPU),
			Type:  pulumi.String("host"), // needed host type to ensure kvm is available on harvester for vms
		},
		BootOrders: pulumi.StringArray{
			pulumi.String("scsi0"), // Disk first
			pulumi.String("ide2"),  // Then CD-ROM with iPXE
			pulumi.String("net0"),  // Then network
		},
		Disks: &vm.VirtualMachineDiskArray{
			&vm.VirtualMachineDiskArgs{
				Interface:   pulumi.String("scsi0"),
				DatastoreId: pulumi.String("local-lvm"), // Create new disk (no cloning for iPXE)
				Size:        pulumi.Int(vmDef.DiskSize),
				FileFormat:  pulumi.String("raw"),
				Iothread:    pulumi.Bool(true),
			},
		},
		Cdrom: &vm.VirtualMachineCdromArgs{
			//	Enabled:   pulumi.Bool(true),
			FileId:    pulumi.String(fmt.Sprintf("nas-storage:iso/%s", isoFileName)),
			Interface: pulumi.String("ide2"),
		},
		NetworkDevices: &vm.VirtualMachineNetworkDeviceArray{
			&vm.VirtualMachineNetworkDeviceArgs{
				Bridge:   pulumi.String("vmbr0"),
				Model:    pulumi.String("virtio"),
				Firewall: pulumi.Bool(true),
			},
		},
		Started: pulumi.Bool(true),
		OnBoot:  pulumi.Bool(false),
		//	Protection: pulumi.Bool(true), Commenting this line for testing. will remove later TODO.
	}, append(opts, pulumi.Protect(true))...)
	if err != nil {
		return nil, err
	}
	return vmInstance, nil
}
