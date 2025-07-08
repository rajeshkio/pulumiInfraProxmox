package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/pulumi/pulumi-command/sdk/go/command/remote"
	"github.com/pulumi/pulumi-local/sdk/go/local"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

var actionHandlers = map[string]ActionHandler{
	"install-haproxy":     handleInstallHAProxy,
	"install-k3s-server":  handleInstallK3Sserver,
	"get-kubeconfig":      handleGetKubeconfig,
	"configure-ipxe-boot": handleConfigureIPXEBoot,
}

func filterEnabledTemplates(ctx *pulumi.Context, templates []VMTemplate, features Features) []VMTemplate {
	var enabled []VMTemplate

	for _, template := range templates {
		//	fmt.Printf("DEBUG: Checking template role: %s\n", template.Role)
		switch template.Role {
		case "loadbalancer":
			if features.Loadbalancer {
				//			fmt.Printf("DEBUG: Including loadbalancer template\n")
				enabled = append(enabled, template)
			}
		case "k3s-server":
			if features.K3s {
				//			fmt.Printf("DEBUG: Including k3s-server template\n")
				enabled = append(enabled, template)
			}
		case "harvester-node":
			if features.Harvester { // Only include if true
				//			fmt.Printf("DEBUG: Including harvester-node template\n")
				enabled = append(enabled, template)
			}
		default:
			enabled = append(enabled, template)
			ctx.Log.Warn(fmt.Sprintf("Unknown role '%s' - including by default", template.Role), nil)
		}
	}
	//	fmt.Printf("DEBUG: Filtered to %d templates\n", len(enabled))
	return enabled
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

	cmd, err := installHaProxy(ctx, lbIP, lbVM, k3sServerIPs)
	if err != nil {
		ctx.Log.Error(fmt.Sprintf("HAProxy installation failed: %v", err), nil)
	}
	actionctx.GlobalDeps["haproxy-install-command"] = cmd

	ctx.Log.Info("HAProxy installation initiated successfully", nil)
	return nil
}

func handleInstallK3Sserver(ctx *pulumi.Context, actionctx ActionContext) error {
	ctx.Log.Info(fmt.Sprintf("Auth method for k3s servers: %s", actionctx.Templates.AuthMethod), nil)
	ctx.Log.Info(fmt.Sprintf("Username: %s", actionctx.Templates.Username), nil)

	lbIPs, ok := actionctx.GlobalDeps["loadbalancer-ips"].([]string)
	if !ok {
		return fmt.Errorf("k3s server needs loadbalancer IP but its not available")
	}

	var haproxyCmd pulumi.Resource
	if haproxyResource, exists := actionctx.GlobalDeps["haproxy-install-command"]; exists {
		if cmd, ok := haproxyResource.(*remote.Command); ok {
			haproxyCmd = cmd
		}
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

			k3sCmd, err := installK3SServer(ctx, lbIP, actionctx.VMPassword, serverIP, serverVM, true, pulumi.String("").ToStringOutput(), haproxyCmd)
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
			_, err := installK3SServer(ctx, lbIP, actionctx.VMPassword, serverIP, serverVM, false, k3sServerToken, haproxyCmd)
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

func handleConfigureIPXEBoot(ctx *pulumi.Context, actionCtx ActionContext) error {
	template := actionCtx.Templates
	config := template.IPXEConfig

	if template.BootMethod != "ipxe" {
		return fmt.Errorf("configure-ipxe-boot action requires bootMethod: ipxe")
	}

	// Simple approach: just log which script file the VM will use
	scriptName := fmt.Sprintf("harvester-%s.ipxe", config.Version)
	scriptURL := fmt.Sprintf("%s/%s", config.BootServerURL, scriptName)

	for _, vmIP := range actionCtx.IPs {
		ctx.Log.Info(fmt.Sprintf("VM %s will boot using script: %s", vmIP, scriptURL), nil)
	}

	return nil
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

	kubeconfigPath := "./kubeconfig.yaml"
	_, err = local.NewFile(ctx, "save-kubeconfig", &local.FileArgs{
		Filename: pulumi.String(kubeconfigPath),
		Content:  cmd.Stdout,
	}, pulumi.DependsOn([]pulumi.Resource{cmd}),
		pulumi.ReplaceOnChanges([]string{"content"}))
	if err != nil {
		return fmt.Errorf("failed to save kubeconfig locally: %w", err)
	}
	ctx.Export("kubeconfig", cmd.Stdout)
	ctx.Log.Info("kubeconfig exported successfully", nil)
	return nil
}

func installHaProxy(ctx *pulumi.Context, lbIP string, vmDependency pulumi.Resource, k3sServerIPs []string) (*remote.Command, error) {

	ctx.Log.Info("Print from installHaProxy", nil)
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
			Host:           pulumi.String(lbIP),
			User:           pulumi.String("rajeshk"),
			PrivateKey:     pulumi.String(os.Getenv("PROXMOX_VE_SSH_PRIVATE_KEY")),
			PerDialTimeout: pulumi.IntPtr(30),
			DialErrorLimit: pulumi.IntPtr(20),
		},
		Create:   pulumi.String(installCmd),
		Triggers: pulumi.Array{pulumi.String("always-run")},
	}, pulumi.DependsOn([]pulumi.Resource{vmDependency}))
	ctx.Log.Info(fmt.Sprintf("install haproxy error %s: ", err), nil)
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
	dependencies := []pulumi.Resource{vmDependency}
	if haproxyDependency != nil {
		dependencies = append(dependencies, haproxyDependency)
		ctx.Log.Info(fmt.Sprintf("K3s server %s will wait for HAProxy installation", serverIP), nil)
	}
	cmd, err := remote.NewCommand(ctx, resourceName, &remote.CommandArgs{
		Connection: &remote.ConnectionArgs{
			Host:       pulumi.String(serverIP),
			User:       pulumi.String("rajeshk"),
			PrivateKey: pulumi.String(os.Getenv("PROXMOX_VE_SSH_PRIVATE_KEY")),
			Password:   pulumi.String(vmPassword),
		},
		Create: k3sCommand,
	}, pulumi.DependsOn(dependencies))
	return cmd, err
}

func getK3sToken(ctx *pulumi.Context, firstServerIP, vmPassword string, vmDependency pulumi.Resource) (*remote.Command, error) {
	resourceName := fmt.Sprintf("k3s-token-%s", strings.ReplaceAll(firstServerIP, ".", "-"))
	cmd, err := remote.NewCommand(ctx, resourceName, &remote.CommandArgs{
		Connection: &remote.ConnectionArgs{
			Host:       pulumi.String(firstServerIP),
			User:       pulumi.String("rajeshk"),
			PrivateKey: pulumi.String(os.Getenv("PROXMOX_VE_SSH_PRIVATE_KEY")),
			Password:   pulumi.String(vmPassword),
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

func getK3sKubeconfig(ctx *pulumi.Context, template VMTemplate, serverIP, vmPassword, lbIP string, vmDependency pulumi.Resource) (*remote.Command, error) {
	resourceName := fmt.Sprintf("k3s-kubeconfig-%s", strings.ReplaceAll(serverIP, ".", "-"))

	kubeconfigCommand := fmt.Sprintf(`
		while [ ! -f /etc/rancher/k3s/k3s.yaml ]; do
			echo "Waiting for kubeconfig..." >&2 
			sleep 5
		done
		sleep 2

		sudo cat /etc/rancher/k3s/k3s.yaml | sed 's/127.0.0.1:6443/%s:6443/g'`, lbIP)

	cmd, err := remote.NewCommand(ctx, resourceName, &remote.CommandArgs{
		Connection: &remote.ConnectionArgs{
			Host:       pulumi.String(serverIP),
			User:       pulumi.String(template.Username),
			PrivateKey: pulumi.String(os.Getenv("PROXMOX_VE_SSH_PRIVATE_KEY")),
			Password:   pulumi.String(vmPassword),
		},
		Create: pulumi.String(kubeconfigCommand),
	}, pulumi.DependsOn([]pulumi.Resource{vmDependency}))
	return cmd, err
}
