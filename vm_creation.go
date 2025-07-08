package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/muhlba91/pulumi-proxmoxve/sdk/v7/go/proxmoxve"
	"github.com/muhlba91/pulumi-proxmoxve/sdk/v7/go/proxmoxve/vm"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func createVMFromTemplate(ctx *pulumi.Context, provider *proxmoxve.Provider, vmIndex int64, template VMTemplate, nodeName, gateway, password string) (*vm.VirtualMachine, error) {

	//	ctx.Log.Info(fmt.Sprintf("Creating VM with auth-method: %s, username: %s, password: %s", template.AuthMethod, template.Username, password), nil)
	//	ctx.Log.Info(fmt.Sprintf("Template debug - Role: %s, AuthMethod: '%s', Username: %s", template.Role, template.AuthMethod, template.Username), nil)

	ctx.Log.Info(fmt.Sprintf("Creating VM %s on node %s",
		fmt.Sprintf("%s-%d", template.VMName, vmIndex), nodeName), nil)
	switch template.BootMethod {
	case "ipxe":
		return createIPXEVM(ctx, provider, vmIndex, template, nodeName, gateway, password)
	case "cloud-init":
		return createCloudInitVM(ctx, provider, vmIndex, template, nodeName, gateway, password)
	default:
		return nil, fmt.Errorf("unsupported boot method: %s", template.BootMethod)
	}
}

func createCloudInitVM(ctx *pulumi.Context, provider *proxmoxve.Provider, vmIndex int64, template VMTemplate, nodeName, gateway, password string) (*vm.VirtualMachine, error) {

	var userAccount *vm.VirtualMachineInitializationUserAccountArgs
	if template.AuthMethod == "ssh-key" {
		sshKey := strings.TrimSpace(os.Getenv("SSH_PUBLIC_KEY"))
		//		ctx.Log.Info(fmt.Sprintf("SSH KEY from env first 100 char: %s", sshKey[:100]), nil)
		//		ctx.Log.Info(fmt.Sprintf("SSH KEY length: %d", len(sshKey)), nil)
		userAccount = &vm.VirtualMachineInitializationUserAccountArgs{
			Username: pulumi.String(template.Username),
			Keys: pulumi.StringArray{
				pulumi.String(sshKey),
			},
		}
	} else {
		// For SLE VMs: Use password authentication
		userAccount = &vm.VirtualMachineInitializationUserAccountArgs{
			Username: pulumi.String(template.Username),
			Password: pulumi.String(password),
		}
	}

	var ipConfig *vm.VirtualMachineInitializationIpConfigArray
	if template.IPConfig == "static" {
		ctx.Export(fmt.Sprintf("vmIndex:%d", vmIndex), nil)
		ctx.Export(fmt.Sprintf("len of template.IPs:%d", len(template.IPs)), nil)
		if vmIndex >= int64(len(template.IPs)) {
			return nil, fmt.Errorf("not enough IPs provided for VM %d", vmIndex)
		}
		ipConfig = &vm.VirtualMachineInitializationIpConfigArray{
			&vm.VirtualMachineInitializationIpConfigArgs{
				Ipv4: vm.VirtualMachineInitializationIpConfigIpv4Args{
					Address: pulumi.String(template.IPs[vmIndex] + "/24"),
					Gateway: pulumi.String(gateway),
				},
			},
		}
	} else {
		ipConfig = nil
	}
	vmName := fmt.Sprintf("%s-%d", template.VMName, vmIndex)

	vmInstance, err := vm.NewVirtualMachine(ctx, template.Name+fmt.Sprintf("-%d", vmIndex), &vm.VirtualMachineArgs{
		Name:     pulumi.String(vmName),
		NodeName: pulumi.String(nodeName),
		Memory: &vm.VirtualMachineMemoryArgs{
			Dedicated: pulumi.Int(template.Memory),
		},
		Cpu: &vm.VirtualMachineCpuArgs{
			Cores: pulumi.Int(template.CPU),
			Type:  pulumi.String("x86-64-v2-AES"),
		},
		Clone: &vm.VirtualMachineCloneArgs{
			NodeName: pulumi.String(nodeName),
			VmId:     pulumi.Int(template.ID),
		},
		Disks: &vm.VirtualMachineDiskArray{
			&vm.VirtualMachineDiskArgs{
				Interface: pulumi.String("scsi0"),
				//	DatastoreId: pulumi.String("nfs-iso"),
				Size:       pulumi.Int(template.DiskSize), // Match your template's disk size
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
		pulumi.IgnoreChanges([]string{"clone"}))
	if err != nil {
		return nil, err
	}
	return vmInstance, nil
}

func createIPXEVM(ctx *pulumi.Context, provider *proxmoxve.Provider, vmIndex int64, template VMTemplate, nodeName, gateway, password string) (*vm.VirtualMachine, error) {
	if template.IPXEConfig == nil {
		return nil, fmt.Errorf("iPXE boot method requires ipxeconfig")
	}
	vmName := fmt.Sprintf("%s-%d", template.VMName, vmIndex)

	isoFileName := "harvester-ipxe.iso"
	if template.IPXEConfig.ISOFileName != "" {
		isoFileName = template.IPXEConfig.ISOFileName
	}

	//var ipConfig *vm.VirtualMachineInitializationIpConfigArray
	vmInstance, err := vm.NewVirtualMachine(ctx, template.Name+fmt.Sprintf("-%d", vmIndex), &vm.VirtualMachineArgs{
		Name:     pulumi.String(vmName),
		NodeName: pulumi.String(nodeName),
		Memory: &vm.VirtualMachineMemoryArgs{
			Dedicated: pulumi.Int(template.Memory),
		},
		Cpu: &vm.VirtualMachineCpuArgs{
			Cores: pulumi.Int(template.CPU),
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
				Size:       pulumi.Int(template.DiskSize), // Match your template's disk size
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
