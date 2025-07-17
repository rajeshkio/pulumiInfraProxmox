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

func isProxmoxLockError(err error) bool {
	if err == nil {
		return false
	}

	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "cfs-lock") || strings.Contains(errStr, "lock request timeout") || strings.Contains(errStr, "storage") && strings.Contains(errStr, "lock")
}

func createVMWithRetry(ctx *pulumi.Context, provider *proxmoxve.Provider, vmIndex int64, vmDef VM, nodeName, gateway, password string, maxRetries int) (*vm.VirtualMachine, error) {
	for attempt := 1; attempt <= maxRetries; attempt++ {
		ctx.Log.Info(fmt.Sprintf("Creating VM %s-%d (attempt %d/%d)", vmDef.Name, vmIndex, attempt, maxRetries), nil)
		vm, err := createVM(ctx, provider, vmIndex, vmDef, nodeName, gateway, password)

		if err == nil {
			if attempt > 1 {
				ctx.Log.Info(fmt.Sprintf("VM %s-%d created successfully after %d attempts", vmDef.Name, vmIndex, attempt), nil)
			}
			return vm, nil
		}
		if isProxmoxLockError(err) && attempt < maxRetries {
			waitTime := time.Duration(attempt*30) * time.Second
			ctx.Log.Warn(fmt.Sprintf("Proxmox lock error on attempt %d: %v", attempt, err), nil)
			ctx.Log.Info(fmt.Sprintf("Waiting %v before retry %d/%d...", waitTime, attempt+1, maxRetries), nil)
			time.Sleep(waitTime)
			continue
		}
		if attempt == maxRetries {
			return nil, fmt.Errorf("failed to create VM after %d attempts (last error: %w)", maxRetries, err)
		} else {
			return nil, fmt.Errorf("non-retryable error creating VM: %w", err)
		}
	}
	return nil, fmt.Errorf("unexpected end of retry loop")
}

func createVM(ctx *pulumi.Context, provider *proxmoxve.Provider, vmIndex int64, vmDef VM, nodeName, gateway, password string) (*vm.VirtualMachine, error) {

	//	ctx.Log.Info(fmt.Sprintf("Creating VM with auth-method: %s, username: %s, password: %s", template.AuthMethod, template.Username, password), nil)
	//	ctx.Log.Info(fmt.Sprintf("Template debug - Role: %s, AuthMethod: '%s', Username: %s", template.Role, template.AuthMethod, template.Username), nil)

	ctx.Log.Info(fmt.Sprintf("Creating VM %s on node %s (method: %s)",
		fmt.Sprintf("%s-%d", vmDef.Name, vmIndex), nodeName, vmDef.BootMethod), nil)

	switch vmDef.BootMethod {
	case "ipxe":
		return createIPXEVM(ctx, provider, vmIndex, vmDef, nodeName, gateway, password)
	case "cloud-init":
		return createCloudInitVM(ctx, provider, vmIndex, vmDef, nodeName, gateway, password)
	default:
		return nil, fmt.Errorf("unsupported boot method: %s", vmDef.BootMethod)
	}
}

func createCloudInitVM(ctx *pulumi.Context, provider *proxmoxve.Provider, vmIndex int64, vmDef VM, nodeName, gateway, password string) (*vm.VirtualMachine, error) {

	var userAccount *vm.VirtualMachineInitializationUserAccountArgs
	if vmDef.AuthMethod == "ssh-key" {
		sshKey := strings.TrimSpace(os.Getenv("SSH_PUBLIC_KEY"))
		//		ctx.Log.Info(fmt.Sprintf("SSH KEY from env first 100 char: %s", sshKey[:100]), nil)
		//		ctx.Log.Info(fmt.Sprintf("SSH KEY length: %d", len(sshKey)), nil)
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
			NodeName: pulumi.String(nodeName),
			VmId:     pulumi.Int(vmDef.TemplateID),
		},
		Disks: &vm.VirtualMachineDiskArray{
			&vm.VirtualMachineDiskArgs{
				Interface: pulumi.String("scsi0"),
				//	DatastoreId: pulumi.String("nfs-iso"),
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
			DatastoreId: pulumi.String("nfs-iso"),
			//	UserDataFileId: pulumi.String("nfs-iso:snippets/test-basic.yaml"),
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
	}, pulumi.Provider(provider),
		pulumi.DeleteBeforeReplace(true),
		pulumi.IgnoreChanges([]string{"clone"}),
		pulumi.Timeouts(&pulumi.CustomTimeouts{
			Create: "15m",
		}))
	if err != nil {
		return nil, err
	}
	return vmInstance, nil
}

func createIPXEVM(ctx *pulumi.Context, provider *proxmoxve.Provider, vmIndex int64, vmDef VM, nodeName, gateway, password string) (*vm.VirtualMachine, error) {
	if vmDef.IPXEConfig == nil {
		return nil, fmt.Errorf("iPXE boot method requires ipxeconfig")
	}
	vmName := fmt.Sprintf("%s-%d", vmDef.Name, vmIndex)

	isoFileName := "harvester-ipxe.iso"
	if vmDef.IPXEConfig.ISOFileName != "" {
		isoFileName = vmDef.IPXEConfig.ISOFileName
	}

	//var ipConfig *vm.VirtualMachineInitializationIpConfigArray
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
		BootOrders: pulumi.StringArray{
			pulumi.String("scsi0"), // Disk first
			pulumi.String("ide2"),  // Then CD-ROM with iPXE
			pulumi.String("net0"),  // Then network
		},
		Disks: &vm.VirtualMachineDiskArray{
			&vm.VirtualMachineDiskArgs{
				Interface: pulumi.String("scsi0"),
				//	DatastoreId: pulumi.String("nfs-iso"),
				//	FileId:     pulumi.String("nfs-iso:iso/harvester-boot.iso"),
				Size:       pulumi.Int(vmDef.DiskSize), // Match your template's disk size
				FileFormat: pulumi.String("raw"),
				Iothread:   pulumi.Bool(true),
			},
		},
		Cdrom: &vm.VirtualMachineCdromArgs{
			//	Enabled:   pulumi.Bool(true),
			FileId:    pulumi.String(fmt.Sprintf("nfs-iso:iso/%s", isoFileName)),
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
	}, pulumi.Provider(provider),
		pulumi.DeleteBeforeReplace(true))
	if err != nil {
		return nil, err
	}
	return vmInstance, nil
}
