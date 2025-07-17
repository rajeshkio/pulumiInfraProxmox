package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/pulumi/pulumi-command/sdk/go/command/remote"
	"github.com/pulumi/pulumi-local/sdk/go/local"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

var serviceHandlers = map[string]ServiceHandler{
	"k3s": handleK3sService,
	//	"kubeadm":   handleKubeadmService,
	"haproxy": handleHAProxyService,
	// "harvester": handleHarvesterService,
	// "rke2":      handleRKE2Service,
	// "talos":     handleTalosService,
}

func handleHAProxyService(ctx *pulumi.Context, serviceCtx ServiceContext) error {
	ctx.Log.Info(fmt.Sprintf("Installing HAProxy service on %d VMs", len(serviceCtx.VMs)), nil)

	backendDiscovery := serviceCtx.ServiceConfig.BackendDiscovery
	if backendDiscovery == "" {
		return fmt.Errorf("HAProxy service requires backend-discovery configuration")
	}
	ctx.Log.Info(fmt.Sprintf("Looking for backend IPs from: %s", backendDiscovery), nil)

	backendIPs, ok := serviceCtx.GlobalDeps[backendDiscovery+"-ips"].([]string)
	if !ok {
		return fmt.Errorf("HAProxy needs backend IPs from '%s' but they are not available", backendDiscovery)
	}

	for i, lbVM := range serviceCtx.VMs {
		lbIP := serviceCtx.IPs[i]
		ctx.Log.Info(fmt.Sprintf("Installing HAProxy on %s with backends: %v", lbIP, backendIPs), nil)
		cmd, err := installHaProxy(ctx, lbIP, lbVM, backendIPs, serviceCtx.VMPassword)
		if err != nil {
			return fmt.Errorf("HAProxy installation failed on %s: %w", lbIP, err)
		}
		serviceCtx.GlobalDeps["haproxy-install-command"] = cmd

	}
	ctx.Log.Info("HAProxy service installed successfully", nil)
	return nil
}

func handleK3sService(ctx *pulumi.Context, serviceCtx ServiceContext) error {
	ctx.Log.Info(fmt.Sprintf("Installing K3s service on %d VMs", len(serviceCtx.VMs)), nil)

	lbIPs, ok := serviceCtx.GlobalDeps["load-balancer-ips"].([]string)
	if !ok {
		return fmt.Errorf("k3s server needs loadbalancer IP but they are not available")
	}
	lbIP := lbIPs[0]

	ctx.Log.Info(fmt.Sprintf("installing k3s server with LBIP: %s", lbIP), nil)
	//var k3sCommands []*remote.Command
	var k3sServerToken pulumi.StringOutput
	var firstServerIP string

	for i, serverVM := range serviceCtx.VMs {
		serverIP := serviceCtx.IPs[i]
		isFirstServer := (i == 0)

		ctx.Log.Info(fmt.Sprintf("Installing K3s on server %d: %s", i+1, serverIP), nil)

		if isFirstServer {
			firstServerIP = serverIP
			ctx.Log.Info(fmt.Sprintf("installing k3s on server %d: %s", i+1, serverIP), nil)

			k3sCmd, err := installK3SServer(ctx, lbIP, serviceCtx.VMPassword, serverIP, serverVM, true, pulumi.String("").ToStringOutput(), nil)
			if err != nil {
				return fmt.Errorf("cannot install K3s server on first node %s: %w", serverIP, err)
			}
			//	k3sCommands = append(k3sCommands, k3sCmd)
			tokenCmd, err := getK3sToken(ctx, serverIP, serviceCtx.VMPassword, k3sCmd)
			if err != nil {
				return fmt.Errorf("cannot get k3s token: %w", err)
			}
			k3sServerToken = tokenCmd.Stdout
		} else {
			_, err := installK3SServer(ctx, lbIP, serviceCtx.VMPassword, serverIP, serverVM, false, k3sServerToken, nil)
			if err != nil {
				return fmt.Errorf("cannot install k3s on server %s: %w", serverIP, serverVM)
			}
			//		k3sCommands = append(k3sCommands, k3sCmds)
		}
	}
	if firstServerIP != "" {
		err := getK3sKubeconfig(ctx, firstServerIP, serviceCtx.VMPassword, lbIP, serviceCtx.VMs[0])
		if err != nil {
			return fmt.Errorf("failed to extract kubeconfig: %w", err)
		}
	}

	ctx.Log.Info(fmt.Sprintf("K3s service installed on %d servers", len(serviceCtx.VMs)), nil)
	return nil
}

// func handleConfigureIPXEBoot(ctx *pulumi.Context, actionCtx ActionContext) error {
// 	template := actionCtx.Templates
// 	config := template.IPXEConfig

// 	if template.BootMethod != "ipxe" {
// 		return fmt.Errorf("configure-ipxe-boot action requires bootMethod: ipxe")
// 	}

// 	// Simple approach: just log which script file the VM will use
// 	scriptName := fmt.Sprintf("harvester-%s.ipxe", config.Version)
// 	scriptURL := fmt.Sprintf("%s/%s", config.BootServerURL, scriptName)

// 	for _, vmIP := range actionCtx.IPs {
// 		ctx.Log.Info(fmt.Sprintf("VM %s will boot using script: %s", vmIP, scriptURL), nil)
// 	}

// 	return nil
// }

func installHaProxy(ctx *pulumi.Context, lbIP string, vmDependency pulumi.Resource, backendIPs []string, vmPassword string) (*remote.Command, error) {

	ctx.Log.Info("Print from installHaProxy", nil)
	var backendServers strings.Builder
	for i, serverIP := range backendIPs {
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

func getK3sKubeconfig(ctx *pulumi.Context, serverIP, vmPassword, lbIP string, vmDependency pulumi.Resource) error {
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
			User:       pulumi.String("rajeshk"),
			PrivateKey: pulumi.String(os.Getenv("PROXMOX_VE_SSH_PRIVATE_KEY")),
			Password:   pulumi.String(vmPassword),
		},
		Create: pulumi.String(kubeconfigCommand),
	}, pulumi.DependsOn([]pulumi.Resource{vmDependency}))

	if err != nil {
		return err
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
