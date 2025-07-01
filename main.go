package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/muhlba91/pulumi-proxmoxve/sdk/v7/go/proxmoxve"
	"github.com/muhlba91/pulumi-proxmoxve/sdk/v7/go/proxmoxve/vm"
	"github.com/pulumi/pulumi-command/sdk/go/command/remote"
	"github.com/pulumi/pulumi-local/sdk/go/local"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

type Action struct {
	Type      string                 `yaml:"type"`
	DependsOn []string               `yaml:"depends-on,omitempty"`
	Config    map[string]interface{} `yaml:"config,omitempty"`
}

type ActionContext struct {
	VMs          []*vm.VirtualMachine
	IPs          []string
	GlobalDeps   map[string]interface{} // Results from other actions/roles
	ActionConfig map[string]interface{} // Config from YAML
	VMPassword   string
	Templates    VMTemplate
}

type VMTemplate struct {
	Name       string   `yaml:"name"`
	VMName     string   `yaml:"vmName"`
	ID         int64    `yaml:"id"`
	DiskSize   int64    `yaml:"disksize"`
	Memory     int64    `yaml:"memory"`
	CPU        int64    `yaml:"cpu"`
	IPConfig   string   `yaml:"ipconfig"`
	IPs        []string `yaml:"ips,omitempty"`
	Gateway    string   `yaml:"gateway"`
	Username   string   `yaml:"username"`
	Password   string   `yaml:"password,omitempty"` // global password
	AuthMethod string   `yaml:"authMethod"`
	Count      int64    `yaml:"count,omitempty"`
	Role       string   `yaml:"role,omitempty"` // NEW!
	Actions    []Action `yaml:"actions,omitempty"`
}

type RoleGroup struct {
	VMs []*vm.VirtualMachine
	IPs []string
}
type ActionHandler func(ctx *pulumi.Context, actionctx ActionContext) error

var actionHandlers = map[string]ActionHandler{
	"install-haproxy":    handleInstallHAProxy,
	"install-k3s-server": handleInstallK3Sserver,
	"get-kubeconfig":     handleGetKubeconfig,
}

func handleInstallHAProxy(ctx *pulumi.Context, actionctx ActionContext) error {

	k3sServerIPs, ok := actionctx.GlobalDeps["k3s-server-ips"].([]string)
	if !ok {
		return fmt.Errorf("haproxy needs k3s server ips but they are not available")
	}
	lbIP := actionctx.IPs[0]
	lbVM := actionctx.VMs[0]

	ctx.Log.Info(fmt.Sprintf("installing haproxy on %s with backends: %v", lbIP, k3sServerIPs), nil)
	//	ctx.Log.Info(fmt.Sprintf("VM dependency: %v", lbVM.ID()), nil)

	_, err := installHaProxy(ctx, lbIP, lbVM, k3sServerIPs)
	if err != nil {
		ctx.Log.Error(fmt.Sprintf("HAProxy installation failed: %v", err), nil)
	}
	return err
}

func handleInstallK3Sserver(ctx *pulumi.Context, actionctx ActionContext) error {
	lbIPs, ok := actionctx.GlobalDeps["loadbalancer-ips"].([]string)
	if !ok {
		return fmt.Errorf("k3s server needs loadbalancer IP but its not available")
	}

	lbIP := lbIPs[0]
	ctx.Log.Info(fmt.Sprintf("installing k3s server with LBIP: %s", lbIP), nil)
	//var k3sCommands []*remote.Command
	var k3sServerToken pulumi.StringOutput
	var firstServerIP string

	for i, serverVM := range actionctx.VMs {
		serverIP := actionctx.IPs[i]
		isFirstServer := (i == 0)

		ctx.Log.Info(fmt.Sprintf("Installing K3s on server %d: %s", i+1, serverIP), nil)

		if isFirstServer {
			firstServerIP = serverIP
			ctx.Log.Info(fmt.Sprintf("installing k3s on server %d: %s", i+1, serverIP), nil)

			k3sCmd, err := installK3SServer(ctx, lbIP, actionctx.VMPassword, serverIP, serverVM, true, pulumi.String("").ToStringOutput(), nil)
			if err != nil {
				return fmt.Errorf("cannot install K3s server on first node %s: %w", serverIP, err)
			}
			//	k3sCommands = append(k3sCommands, k3sCmd)
			tokenCmd, err := getK3sToken(ctx, serverIP, actionctx.VMPassword, k3sCmd)
			if err != nil {
				return fmt.Errorf("cannot get k3s token: %w", err)
			}
			k3sServerToken = tokenCmd.Stdout
		} else {
			_, err := installK3SServer(ctx, lbIP, actionctx.VMPassword, serverIP, serverVM, false, k3sServerToken, nil)
			if err != nil {
				return fmt.Errorf("cannot install k3s on server %s: %w", serverIP, serverVM)
			}
			//		k3sCommands = append(k3sCommands, k3sCmds)
		}
	}

	actionctx.GlobalDeps["k3s-first-server-ip"] = firstServerIP
	actionctx.GlobalDeps["k3s-loadbalancer-ip"] = lbIP
	ctx.Log.Info(fmt.Sprintf("K3s installation initiated on %d servers", len(actionctx.VMs)), nil)
	return nil
}

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

func createVMFromTemplate(ctx *pulumi.Context, provider *proxmoxve.Provider, vmIndex int64, template VMTemplate, nodeName, gateway, password string) (*vm.VirtualMachine, error) {
	var userAccount *vm.VirtualMachineInitializationUserAccountArgs

	//	ctx.Log.Info(fmt.Sprintf("Creating VM with auth-method: %s, username: %s, password: %s", template.AuthMethod, template.Username, password), nil)
	//	ctx.Log.Info(fmt.Sprintf("Template debug - Role: %s, AuthMethod: '%s', Username: %s", template.Role, template.AuthMethod, template.Username), nil)

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
func installHaProxy(ctx *pulumi.Context, lbIP string, vmDependency pulumi.Resource, k3sServerIPs []string) (*remote.Command, error) {

	var backendServers strings.Builder
	for i, serverIP := range k3sServerIPs {
		backendServers.WriteString(fmt.Sprintf("    server k3s-server-%d %s:6443 check\n", i+1, serverIP))
	}

	haProxyConfig := fmt.Sprintf(`
global
    daemon
    maxconn 4096
    log stdout local0

defaults
    mode tcp
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms
    option tcplog
    log global

# K3s API Server Load Balancer
frontend k3s-api
    bind *:6443
    mode tcp
    default_backend k3s-servers

backend k3s-servers
    mode tcp
    balance roundrobin
%s
	`, backendServers.String())

	installCmd := fmt.Sprintf(`
		# Update package list
		sudo apt update
		
		# Install HAProxy
		sudo apt install -y haproxy
		
		# Backup original config
		sudo cp /etc/haproxy/haproxy.cfg /etc/haproxy/haproxy.cfg.backup
		
		# Create new HAProxy configuration
		sudo tee /etc/haproxy/haproxy.cfg << 'EOF'
%s
EOF
		
		# Enable and start HAProxy
		sudo systemctl enable haproxy
		sudo systemctl restart haproxy
		
		# Check HAProxy status
		sudo systemctl status haproxy --no-pager
		
		echo "HAProxy installed and configured successfully"
		echo "K3s API accessible via: https://%s:6443"
	`, haProxyConfig, lbIP)

	resourceName := fmt.Sprintf("haproxy-install-%s", strings.ReplaceAll(lbIP, ".", "-"))

	cmd, err := remote.NewCommand(ctx, resourceName, &remote.CommandArgs{
		Connection: &remote.ConnectionArgs{
			Host:       pulumi.String(lbIP),
			User:       pulumi.String("rajeshk"),
			PrivateKey: pulumi.String(os.Getenv("PROXMOX_VE_SSH_PRIVATE_KEY")),
		},
		Create: pulumi.String(installCmd),
	}, pulumi.DependsOn([]pulumi.Resource{vmDependency}))
	return cmd, err
}

func installK3SServer(ctx *pulumi.Context, lbIP, vmPassword, serverIP string, vmDependency pulumi.Resource, isFirstServer bool, k3sToken pulumi.StringOutput, haproxyDependency pulumi.Resource) (*remote.Command, error) {
	var k3sCommand pulumi.StringInput

	if isFirstServer {
		k3sCommand = pulumi.Sprintf(`
			sudo tee /etc/resolv.conf << 'EOF'
nameserver 192.168.90.1
EOF
			curl -sfL https://get.k3s.io | sudo sh -s - server \
				--cluster-init --tls-san=%s --tls-san=$(hostname -I | awk '{print $1}') \
				--write-kubeconfig-mode 644
			sudo systemctl enable --now k3s
			sleep 100
			sudo k3s kubectl wait --for=condition=Ready nodes --all --timeout=300s
			sudo ls /var/lib/rancher/k3s/server/node-token
				`, lbIP)
	} else {
		k3sCommand = pulumi.Sprintf(`
			sudo tee /etc/resolv.conf << 'EOF'
nameserver 192.168.90.1
EOF
			# Wait for first server to be ready
			until curl -k -s https://%s:6443/ping; do
				echo "Waiting for first K3s server to be ready..."
				sleep 10
			done
			
			curl -sfL https://get.k3s.io | sudo sh -s - server \
			--server https://%s:6443 \
			--token %s \
			--tls-san=%s --tls-san=$(hostname -I | awk '{print $1}') \
			--write-kubeconfig-mode 644
			sudo systemctl enable --now k3s
			echo "K3s server joined cluster successfully"
		`, lbIP, lbIP, k3sToken, lbIP)
	}
	resourceName := fmt.Sprintf("k3s-server-%s", strings.ReplaceAll(serverIP, ".", "-"))
	cmd, err := remote.NewCommand(ctx, resourceName, &remote.CommandArgs{
		Connection: &remote.ConnectionArgs{
			Host:     pulumi.String(serverIP),
			User:     pulumi.String("rajeshk"),
			Password: pulumi.String(vmPassword),
		},
		Create: k3sCommand,
	}, pulumi.DependsOn([]pulumi.Resource{vmDependency}))
	return cmd, err
}

func getK3sToken(ctx *pulumi.Context, firstServerIP, vmPassword string, vmDependency pulumi.Resource) (*remote.Command, error) {
	resourceName := fmt.Sprintf("k3s-token-%s", strings.ReplaceAll(firstServerIP, ".", "-"))
	cmd, err := remote.NewCommand(ctx, resourceName, &remote.CommandArgs{
		Connection: &remote.ConnectionArgs{
			Host:     pulumi.String(firstServerIP),
			User:     pulumi.String("rajeshk"),
			Password: pulumi.String(vmPassword),
		},
		Create: pulumi.String(`
			# Wait for K3s to be fully ready and token file to exist
			#while [ ! -f /var/lib/rancher/k3s/server/node-token ]; do
			#	echo "Waiting for K3s token file..."
			#	sleep 5
			#done

			# Wait a bit more to ensure K3s is fully initialized
			#sleep 10

			sudo cat /var/lib/rancher/k3s/server/node-token
			`),
	}, pulumi.DependsOn([]pulumi.Resource{vmDependency}))
	return cmd, err
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

func loadConfig(ctx *pulumi.Context) (string, string, string, []VMTemplate, error) {
	cfg := config.New(ctx, "")
	proxmoxNode := cfg.Require("proxmox-node")
	vmPassword := cfg.Require("password")
	gateway := cfg.Require("gateway")

	var templates []VMTemplate
	cfg.RequireObject("vm-templates", &templates)

	ctx.Export("vmPassword", pulumi.String(vmPassword))
	return proxmoxNode, vmPassword, gateway, templates, nil

}
func executeAction(ctx *pulumi.Context, action Action, template VMTemplate, roleGroups RoleGroup, globalDeps map[string]interface{}, vmpassword string) error {
	actionCtx := ActionContext{
		VMs:          roleGroups.VMs,
		IPs:          roleGroups.IPs,
		GlobalDeps:   globalDeps,
		ActionConfig: action.Config,
		VMPassword:   vmpassword,
		Templates:    template,
	}

	if handler, exists := actionHandlers[action.Type]; exists {
		return handler(ctx, actionCtx)
	} else {
		return fmt.Errorf("unknown action type: %s", action.Type)
	}
}
func executeActions(ctx *pulumi.Context, templates []VMTemplate, roleGroups map[string]RoleGroup, globalDeps map[string]interface{}, vmPassword string) error {
	for _, template := range templates {
		roleGroup := roleGroups[template.Role]

		for _, action := range template.Actions {
			ctx.Log.Info(fmt.Sprintf("Executing action '%s' for role '%s'", action.Type, template.Role), nil)

			err := executeAction(ctx, action, template, roleGroup, globalDeps, vmPassword)
			if err != nil {
				return fmt.Errorf("failed to execute action %s for role %s: %w", action.Type, template.Role, err)
			}
		}
	}
	return nil
}

func getK3sKubeconfig(ctx *pulumi.Context, template VMTemplate, serverIP, vmPassword, lbIP string, vmDependency pulumi.Resource) (*remote.Command, error) {
	resourceName := fmt.Sprintf("k3s-kubeconfig-%s", strings.ReplaceAll(serverIP, ".", "-"))

	kubeconfigCommand := fmt.Sprintf(`
		while [ ! -f /etc/rancher/k3s/k3s.yaml ]; do
			echo "Waiting for kubeconfig..."
			sleep 5
		done

		sudo cat /etc/rancher/k3s/k3s.yaml | sed 's/127.0.0.1:6443/%s:6443/g'`, lbIP)

	cmd, err := remote.NewCommand(ctx, resourceName, &remote.CommandArgs{
		Connection: &remote.ConnectionArgs{
			Host:     pulumi.String(serverIP),
			User:     pulumi.String(template.Username),
			Password: pulumi.String(vmPassword),
		},
		Create: pulumi.String(kubeconfigCommand),
	}, pulumi.DependsOn([]pulumi.Resource{vmDependency}))
	return cmd, err
}

func handleGetKubeconfig(ctx *pulumi.Context, actionctx ActionContext) error {
	lbIPs, ok := actionctx.GlobalDeps["loadbalancer-ips"].([]string)
	if !ok {
		return fmt.Errorf("kubeconfig needs loadbalancer IP but it's not available")
	}

	firstServerIP := actionctx.IPs[0]
	firstServerVM := actionctx.VMs[0]
	lbIP := lbIPs[0]

	ctx.Log.Info(fmt.Sprintf("Getting kubeconfig from %s with LB IP %s", firstServerIP, lbIP), nil)
	cmd, err := getK3sKubeconfig(ctx, actionctx.Templates, firstServerIP, actionctx.VMPassword, lbIP, firstServerVM)
	if err != nil {
		return fmt.Errorf("failed to get kubeconfig: %w", err)
	}

	kubeconfigPath := "./kubeconfig"
	_, err = local.NewFile(ctx, "save-kubeconfig", &local.FileArgs{
		Filename: pulumi.String(kubeconfigPath),
		Content:  cmd.Stdout,
	}, pulumi.DependsOn([]pulumi.Resource{cmd}))
	if err != nil {
		return fmt.Errorf("failed to save kubeconfig locally: %w", err)
	}
	ctx.Export("kubeconfig", cmd.Stdout)
	ctx.Log.Info("kubeconfig exported successfully", nil)
	return nil
}
func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {

		if err := checkRequiredEnvVars(); err != nil {
			return err
		}
		provider, err := setupProxmoxProvider(ctx)
		if err != nil {
			return fmt.Errorf("failed to setup Proxmox provider: %w", err)
		}
		fmt.Println(provider)

		proxmoxNode, vmPassword, gateway, templates, err := loadConfig(ctx)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		var allVMs []*vm.VirtualMachine
		for _, template := range templates {
			count := template.Count
			if count == 0 {
				count = 1
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

		roleGroups, err := groupVMsByRole(allVMs, templates)
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

		err = executeActions(ctx, templates, roleGroups, globalDeps, vmPassword)
		if err != nil {
			return fmt.Errorf("failed to execute actions %s", err)
		}
		return nil
	})
}
