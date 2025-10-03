package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/muhlba91/pulumi-proxmoxve/sdk/v7/go/proxmoxve/vm"
	"github.com/pulumi/pulumi-command/sdk/go/command/remote"
	"github.com/pulumi/pulumi-local/sdk/go/local"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

var serviceHandlers = map[string]ServiceHandler{
	"k3s":       handleK3sService,
	"rke2":      handleRKE2Service,
	"haproxy":   handleHAProxyService,
	"kubeadm":   handleKubeadmService,
	"harvester": handleHarvesterService,
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

// RKE2 Service Handler
func handleRKE2Service(ctx *pulumi.Context, serviceCtx ServiceContext) error {
	ctx.Log.Info(fmt.Sprintf("Installing RKE2 service on %d VMs", len(serviceCtx.VMs)), nil)

	lbIPs, ok := serviceCtx.GlobalDeps["load-balancer-ips"].([]string)
	if !ok {
		return fmt.Errorf("rke2 server needs loadbalancer IP but they are not available")
	}
	lbIP := lbIPs[0]

	ctx.Log.Info(fmt.Sprintf("installing rke2 server with LBIP: %s", lbIP), nil)
	var rke2ServerToken pulumi.StringOutput
	var firstServerIP string

	for i, serverVM := range serviceCtx.VMs {
		serverIP := serviceCtx.IPs[i]
		isFirstServer := (i == 0)

		ctx.Log.Info(fmt.Sprintf("Installing RKE2 on server %d: %s", i+1, serverIP), nil)

		if isFirstServer {
			firstServerIP = serverIP
			ctx.Log.Info(fmt.Sprintf("installing rke2 on server %d: %s", i+1, serverIP), nil)

			rke2Cmd, err := installRKE2Server(ctx, lbIP, serviceCtx.VMPassword, serverIP, serverVM, true, pulumi.String("").ToStringOutput(), nil)
			if err != nil {
				return fmt.Errorf("cannot install RKE2 server on first node %s: %w", serverIP, err)
			}
			tokenCmd, err := getRKE2Token(ctx, serverIP, serviceCtx.VMPassword, rke2Cmd)
			if err != nil {
				return fmt.Errorf("cannot get rke2 token: %w", err)
			}
			rke2ServerToken = tokenCmd.Stdout
		} else {
			_, err := installRKE2Server(ctx, lbIP, serviceCtx.VMPassword, serverIP, serverVM, false, rke2ServerToken, nil)
			if err != nil {
				return fmt.Errorf("cannot install rke2 on server %s: %w", serverIP, serverVM)
			}
		}
	}
	if firstServerIP != "" {
		err := getRKE2Kubeconfig(ctx, firstServerIP, serviceCtx.VMPassword, lbIP, serviceCtx.VMs[0])
		if err != nil {
			return fmt.Errorf("failed to extract rke2 kubeconfig: %w", err)
		}
	}

	ctx.Log.Info(fmt.Sprintf("RKE2 service installed on %d servers", len(serviceCtx.VMs)), nil)
	return nil
}

// RKE2-specific installation functions
func installRKE2Server(ctx *pulumi.Context, lbIP, vmPassword, serverIP string, vmDependency pulumi.Resource, isFirstServer bool, rke2Token pulumi.StringOutput, haproxyDependency pulumi.Resource) (*remote.Command, error) {
	var rke2Command pulumi.StringInput

	if isFirstServer {
		// First server - initialize cluster
		rke2Command = pulumi.Sprintf(`
			# Set DNS resolver
			sudo tee /etc/resolv.conf << 'EOF'
nameserver 192.168.90.1
EOF

			# Create RKE2 config directory
			sudo mkdir -p /etc/rancher/rke2

			# Create RKE2 server configuration
			sudo tee /etc/rancher/rke2/config.yaml << 'EOF'
token: bootstrap-token
cluster-init: true
tls-san:
  - %s
  - $(hostname -I | awk '{print $1}')
write-kubeconfig-mode: "0644"
EOF

			# Download and install RKE2
			curl -sfL https://get.rke2.io | sudo sh -

			# Enable and start RKE2 server
			sudo systemctl enable rke2-server.service
			sudo systemctl start rke2-server.service

			# Wait for RKE2 to be ready
			sleep 120
			
			# Wait for all nodes to be ready
			sudo /var/lib/rancher/rke2/bin/kubectl --kubeconfig /etc/rancher/rke2/rke2.yaml wait --for=condition=Ready nodes --all --timeout=300s
		`, lbIP)
	} else {
		// Additional servers - join cluster
		rke2Command = pulumi.Sprintf(`
			# Set DNS resolver
			sudo tee /etc/resolv.conf << 'EOF'
nameserver 192.168.90.1
EOF

			# Wait for first server to be ready
			until curl -k -s https://%s:9345/ping; do
				echo "Waiting for first RKE2 server to be ready..."
				sleep 10
			done

			# Create RKE2 config directory
			sudo mkdir -p /etc/rancher/rke2

			# Create RKE2 server configuration for joining
			sudo tee /etc/rancher/rke2/config.yaml << 'EOF'
server: https://%s:9345
token: %s
tls-san:
  - %s
  - $(hostname -I | awk '{print $1}')
write-kubeconfig-mode: "0644"
EOF

			# Download and install RKE2
			curl -sfL https://get.rke2.io | sudo sh -

			# Enable and start RKE2 server
			sudo systemctl enable rke2-server.service
			sudo systemctl start rke2-server.service

			echo "RKE2 server joined cluster successfully"
		`, lbIP, lbIP, rke2Token, lbIP)
	}

	resourceName := fmt.Sprintf("rke2-server-%s", strings.ReplaceAll(serverIP, ".", "-"))
	dependencies := []pulumi.Resource{vmDependency}
	if haproxyDependency != nil {
		dependencies = append(dependencies, haproxyDependency)
		ctx.Log.Info(fmt.Sprintf("RKE2 server %s will wait for HAProxy installation", serverIP), nil)
	}

	cmd, err := remote.NewCommand(ctx, resourceName, &remote.CommandArgs{
		Connection: &remote.ConnectionArgs{
			Host:       pulumi.String(serverIP),
			User:       pulumi.String("rajeshk"),
			PrivateKey: pulumi.String(os.Getenv("PROXMOX_VE_SSH_PRIVATE_KEY")),
			Password:   pulumi.String(vmPassword),
		},
		Create: rke2Command,
	}, pulumi.DependsOn(dependencies))
	return cmd, err
}

func getRKE2Token(ctx *pulumi.Context, firstServerIP, vmPassword string, vmDependency pulumi.Resource) (*remote.Command, error) {
	resourceName := fmt.Sprintf("rke2-token-%s", strings.ReplaceAll(firstServerIP, ".", "-"))
	cmd, err := remote.NewCommand(ctx, resourceName, &remote.CommandArgs{
		Connection: &remote.ConnectionArgs{
			Host:       pulumi.String(firstServerIP),
			User:       pulumi.String("rajeshk"),
			PrivateKey: pulumi.String(os.Getenv("PROXMOX_VE_SSH_PRIVATE_KEY")),
			Password:   pulumi.String(vmPassword),
		},
		Create: pulumi.String(`
			# Wait for RKE2 to be fully ready and token file to exist
			while [ ! -f /var/lib/rancher/rke2/server/node-token ]; do
				echo "Waiting for RKE2 token file..."
				sleep 5
			done

			# Wait a bit more to ensure RKE2 is fully initialized
			sleep 10

			sudo cat /var/lib/rancher/rke2/server/node-token
		`),
	}, pulumi.DependsOn([]pulumi.Resource{vmDependency}))
	return cmd, err
}

func getRKE2Kubeconfig(ctx *pulumi.Context, serverIP, vmPassword, lbIP string, vmDependency pulumi.Resource) error {
	resourceName := fmt.Sprintf("rke2-kubeconfig-%s", strings.ReplaceAll(serverIP, ".", "-"))

	kubeconfigCommand := fmt.Sprintf(`
		while [ ! -f /etc/rancher/rke2/rke2.yaml ]; do
			echo "Waiting for RKE2 kubeconfig..." >&2 
			sleep 5
		done
		sleep 2
		sudo cat /etc/rancher/rke2/rke2.yaml | sed 's/127.0.0.1:6443/%s:6443/g'`, lbIP)

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

	kubeconfigPath := "./rke2-kubeconfig.yaml"
	_, err = local.NewFile(ctx, "save-rke2-kubeconfig", &local.FileArgs{
		Filename: pulumi.String(kubeconfigPath),
		Content:  cmd.Stdout,
	}, pulumi.DependsOn([]pulumi.Resource{cmd}),
		pulumi.ReplaceOnChanges([]string{"content"}))

	if err != nil {
		return fmt.Errorf("failed to save rke2 kubeconfig locally: %w", err)
	}
	ctx.Export("rke2-kubeconfig", cmd.Stdout)
	ctx.Export("rke2-kubeconfigPath", pulumi.String(kubeconfigPath))
	ctx.Log.Info("RKE2 kubeconfig exported successfully", nil)
	return nil
}

func handleKubeadmService(ctx *pulumi.Context, serviceCtx ServiceContext) error {
	ctx.Log.Info(fmt.Sprintf("Installing Kubeadm Kubernetes cluster"), nil)

	controlPlaneNodes := serviceCtx.ServiceConfig.ControlPlane
	if len(controlPlaneNodes) == 0 {
		return fmt.Errorf("kubeadm service requires control-plane nodes configured")
	}

	// Get control plane VMs and IPs
	var controlPlaneVMs []*vm.VirtualMachine
	var controlPlaneIPs []string
	for _, nodeName := range controlPlaneNodes {
		vms, ok := serviceCtx.GlobalDeps[nodeName+"-vms"]
		if !ok {
			return fmt.Errorf("control plane VMs for '%s' not found", nodeName)
		}
		ips, ok := serviceCtx.GlobalDeps[nodeName+"-ips"].([]string)
		if !ok {
			return fmt.Errorf("control plane IPs for '%s' not found", nodeName)
		}
		controlPlaneVMs = append(controlPlaneVMs, vms.([]*vm.VirtualMachine)...)
		controlPlaneIPs = append(controlPlaneIPs, ips...)
	}

	ctx.Log.Info(fmt.Sprintf("Control plane: %d nodes at %v", len(controlPlaneIPs), controlPlaneIPs), nil)

	// Initialize first control plane node
	firstControlPlaneIP := controlPlaneIPs[0]
	firstControlPlaneVM := controlPlaneVMs[0]

	ctx.Log.Info(fmt.Sprintf("Initializing first control plane node: %s", firstControlPlaneIP), nil)
	joinCommand, err := initKubeadmControlPlane(ctx, firstControlPlaneIP, firstControlPlaneVM, serviceCtx)
	if err != nil {
		return fmt.Errorf("failed to initialize control plane: %w", err)
	}

	// Join additional control plane nodes if any
	for i := 1; i < len(controlPlaneIPs); i++ {
		ctx.Log.Info(fmt.Sprintf("Joining control plane node %d: %s", i, controlPlaneIPs[i]), nil)
		err := joinKubeadmControlPlane(ctx, controlPlaneIPs[i], controlPlaneVMs[i], joinCommand, serviceCtx)
		if err != nil {
			return fmt.Errorf("failed to join control plane node %s: %w", controlPlaneIPs[i], err)
		}
	}

	// Join worker nodes if configured
	workerNodeNames := serviceCtx.ServiceConfig.Workers
	if len(workerNodeNames) > 0 {
		ctx.Log.Info(fmt.Sprintf("Joining %d worker node groups", len(workerNodeNames)), nil)
		for _, nodeName := range workerNodeNames {
			vms, ok := serviceCtx.GlobalDeps[nodeName+"-vms"]
			if !ok {
				continue
			}
			ips, ok := serviceCtx.GlobalDeps[nodeName+"-ips"].([]string)
			if !ok {
				continue
			}

			workerVMs := vms.([]*vm.VirtualMachine)
			for i, workerIP := range ips {
				ctx.Log.Info(fmt.Sprintf("Joining worker node: %s", workerIP), nil)
				err := joinKubeadmWorker(ctx, workerIP, workerVMs[i], joinCommand, serviceCtx)
				if err != nil {
					return fmt.Errorf("failed to join worker %s: %w", workerIP, err)
				}
			}
		}
	}

	ctx.Log.Info("Kubeadm cluster installed successfully", nil)
	return nil
}

func initKubeadmControlPlane(ctx *pulumi.Context, ip string, vmResource *vm.VirtualMachine, serviceCtx ServiceContext) (pulumi.StringOutput, error) {
	ctx.Log.Info(fmt.Sprintf("Hello From initKubeadmControlPlane on ip %s", ip), nil)
	// Get configuration
	config := serviceCtx.ServiceConfig.Config
	podCIDR := getConfigString(config, "pod-cidr", "10.244.0.0/16")
	serviceCIDR := getConfigString(config, "service-cidr", "10.96.0.0/12")

	// Installation script
	installScript := fmt.Sprintf(`#!/bin/bash
set -e

# Disable swap
swapoff -a
sed -i '/ swap / s/^\(.*\)$/#\1/g' /etc/fstab

# Enable IP forwarding and bridging
cat <<EOF | tee /etc/modules-load.d/k8s.conf
overlay
br_netfilter
EOF

modprobe overlay
modprobe br_netfilter

cat <<EOF | tee /etc/sysctl.d/k8s.conf
net.bridge.bridge-nf-call-iptables  = 1
net.bridge.bridge-nf-call-ip6tables = 1
net.ipv4.ip_forward                 = 1
EOF

sysctl --system

# Install containerd
apt-get update
apt-get install -y containerd

# Configure containerd
mkdir -p /etc/containerd
containerd config default | tee /etc/containerd/config.toml
sed -i 's/SystemdCgroup = false/SystemdCgroup = true/' /etc/containerd/config.toml
systemctl restart containerd
systemctl enable containerd

# Install kubeadm, kubelet, kubectl
apt-get update
apt-get install -y apt-transport-https ca-certificates curl gpg

mkdir -p -m 755 /etc/apt/keyrings
curl -fsSL https://pkgs.k8s.io/core:/stable:/v1.34/deb/Release.key | gpg --dearmor -o /etc/apt/keyrings/kubernetes-apt-keyring.gpg

echo 'deb [signed-by=/etc/apt/keyrings/kubernetes-apt-keyring.gpg] https://pkgs.k8s.io/core:/stable:/v1.34/deb/ /' | tee /etc/apt/sources.list.d/kubernetes.list

apt-get update
apt-get install -y kubelet kubeadm kubectl
apt-mark hold kubelet kubeadm kubectl

systemctl enable kubelet

# Initialize control plane
kubeadm init \
  --pod-network-cidr=%s \
  --service-cidr=%s \
  --apiserver-advertise-address=%s \
  --control-plane-endpoint=%s:6443 \
  --upload-certs

# Set up kubeconfig
mkdir -p $HOME/.kube
cp -i /etc/kubernetes/admin.conf $HOME/.kube/config
chown $(id -u):$(id -g) $HOME/.kube/config

# Install Calico CNI
kubectl apply -f https://raw.githubusercontent.com/projectcalico/calico/v3.29.1/manifests/calico.yaml

# Generate join command and save it
kubeadm token create --print-join-command > /tmp/kubeadm-join-command.txt

echo "Control plane initialized successfully"
`, podCIDR, serviceCIDR, ip, ip)

	connection := &remote.ConnectionArgs{
		Host:       pulumi.String(ip),
		User:       pulumi.String("rajeshk"),
		PrivateKey: pulumi.String(os.Getenv("PROXMOX_VE_SSH_PRIVATE_KEY")),
	}

	cmd, err := remote.NewCommand(ctx, fmt.Sprintf("kubeadm-init-%s", ip), &remote.CommandArgs{
		Connection: connection,
		Create:     pulumi.String(installScript),
	}, pulumi.DependsOn([]pulumi.Resource{vmResource}))

	if err != nil {
		return pulumi.StringOutput{}, err
	}

	// Read the join command
	joinCmd, err := remote.NewCommand(ctx, fmt.Sprintf("kubeadm-join-command-%s", ip), &remote.CommandArgs{
		Connection: connection,
		Create:     pulumi.String("cat /tmp/kubeadm-join-command.txt"),
	}, pulumi.DependsOn([]pulumi.Resource{cmd}))

	if err != nil {
		return pulumi.StringOutput{}, err
	}

	return joinCmd.Stdout, nil
}

func joinKubeadmControlPlane(ctx *pulumi.Context, ip string, vmResource *vm.VirtualMachine, joinCommand pulumi.StringOutput, serviceCtx ServiceContext) error {
	joinScript := joinCommand.ApplyT(func(cmd string) string {
		return fmt.Sprintf(`#!/bin/bash
set -e

# Same prerequisites as control plane
swapoff -a
sed -i '/ swap / s/^\(.*\)$/#\1/g' /etc/fstab

cat <<EOF | tee /etc/modules-load.d/k8s.conf
overlay
br_netfilter
EOF

modprobe overlay
modprobe br_netfilter

cat <<EOF | tee /etc/sysctl.d/k8s.conf
net.bridge.bridge-nf-call-iptables  = 1
net.bridge.bridge-nf-call-ip6tables = 1
net.ipv4.ip_forward                 = 1
EOF

sysctl --system

# Install containerd
apt-get update
apt-get install -y containerd
mkdir -p /etc/containerd
containerd config default | tee /etc/containerd/config.toml
sed -i 's/SystemdCgroup = false/SystemdCgroup = true/' /etc/containerd/config.toml
systemctl restart containerd
systemctl enable containerd

# Install kubeadm, kubelet, kubectl
apt-get install -y apt-transport-https ca-certificates curl gpg
mkdir -p -m 755 /etc/apt/keyrings
curl -fsSL https://pkgs.k8s.io/core:/stable:/v1.34/deb/Release.key | gpg --dearmor -o /etc/apt/keyrings/kubernetes-apt-keyring.gpg
echo 'deb [signed-by=/etc/apt/keyrings/kubernetes-apt-keyring.gpg] https://pkgs.k8s.io/core:/stable:/v1.34/deb/ /' | tee /etc/apt/sources.list.d/kubernetes.list
apt-get update
apt-get install -y kubelet kubeadm kubectl
apt-mark hold kubelet kubeadm kubectl
systemctl enable kubelet

# Join as control plane
%s --control-plane
`, cmd)
	}).(pulumi.StringOutput)

	connection := &remote.ConnectionArgs{
		Host:       pulumi.String(ip),
		User:       pulumi.String("rajeshk"),
		PrivateKey: pulumi.String(os.Getenv("PROXMOX_VE_SSH_PRIVATE_KEY")),
	}

	_, err := remote.NewCommand(ctx, fmt.Sprintf("kubeadm-join-cp-%s", ip), &remote.CommandArgs{
		Connection: connection,
		Create:     joinScript,
	}, pulumi.DependsOn([]pulumi.Resource{vmResource}))

	return err
}

func joinKubeadmWorker(ctx *pulumi.Context, ip string, vmResource *vm.VirtualMachine, joinCommand pulumi.StringOutput, serviceCtx ServiceContext) error {
	joinScript := joinCommand.ApplyT(func(cmd string) string {
		return fmt.Sprintf(`#!/bin/bash
set -e

# Prerequisites
swapoff -a
sed -i '/ swap / s/^\(.*\)$/#\1/g' /etc/fstab

cat <<EOF | tee /etc/modules-load.d/k8s.conf
overlay
br_netfilter
EOF

modprobe overlay
modprobe br_netfilter

cat <<EOF | tee /etc/sysctl.d/k8s.conf
net.bridge.bridge-nf-call-iptables  = 1
net.bridge.bridge-nf-call-ip6tables = 1
net.ipv4.ip_forward                 = 1
EOF

sysctl --system

# Install containerd
apt-get update
apt-get install -y containerd
mkdir -p /etc/containerd
containerd config default | tee /etc/containerd/config.toml
sed -i 's/SystemdCgroup = false/SystemdCgroup = true/' /etc/containerd/config.toml
systemctl restart containerd
systemctl enable containerd

# Install kubeadm, kubelet, kubectl
apt-get install -y apt-transport-https ca-certificates curl gpg
mkdir -p -m 755 /etc/apt/keyrings
curl -fsSL https://pkgs.k8s.io/core:/stable:/v1.34/deb/Release.key | gpg --dearmor -o /etc/apt/keyrings/kubernetes-apt-keyring.gpg
echo 'deb [signed-by=/etc/apt/keyrings/kubernetes-apt-keyring.gpg] https://pkgs.k8s.io/core:/stable:/v1.34/deb/ /' | tee /etc/apt/sources.list.d/kubernetes.list
apt-get update
apt-get install -y kubelet kubeadm kubectl
apt-mark hold kubelet kubeadm kubectl
systemctl enable kubelet

# Join as worker
%s
`, cmd)
	}).(pulumi.StringOutput)

	connection := &remote.ConnectionArgs{
		Host:       pulumi.String(ip),
		User:       pulumi.String("rajeshk"),
		PrivateKey: pulumi.String(os.Getenv("PROXMOX_VE_SSH_PRIVATE_KEY")),
	}

	_, err := remote.NewCommand(ctx, fmt.Sprintf("kubeadm-join-worker-%s", ip), &remote.CommandArgs{
		Connection: connection,
		Create:     joinScript,
	}, pulumi.DependsOn([]pulumi.Resource{vmResource}))

	return err
}

// Helper function to get config values with defaults
func getConfigString(config map[string]interface{}, key string, defaultValue string) string {
	if val, ok := config[key]; ok {
		if strVal, ok := val.(string); ok {
			return strVal
		}
	}
	return defaultValue
}

func handleHarvesterService(ctx *pulumi.Context, serviceCtx ServiceContext) error {
	// Harvester is special - VMs boot from iPXE and configure themselves
	// This handler just logs information about Harvester deployment
	
	targets := serviceCtx.ServiceConfig.Targets
	config := serviceCtx.ServiceConfig.Config
	version := getConfigString(config, "version", "v1.4.1")
	bootServerURL := getConfigString(config, "boot-server-url", "http://192.168.90.1:8080")

	// Check if there are any Harvester VMs configured
	var harvesterVMCount int
	for _, targetName := range targets {
		if vms, ok := serviceCtx.GlobalDeps[targetName+"-vms"]; ok {
			harvesterVMCount += len(vms.([]*vm.VirtualMachine))
		}
	}

	if harvesterVMCount == 0 {
		ctx.Log.Info("Harvester service enabled but no VMs with bootMethod: ipxe found - skipping", nil)
		ctx.Log.Info("Note: Harvester VMs auto-configure via iPXE boot - no post-deployment needed", nil)
		return nil
	}

	// Log configuration info
	scriptName := fmt.Sprintf("harvester-%s.ipxe", version)
	scriptURL := fmt.Sprintf("%s/%s", bootServerURL, scriptName)

	ctx.Log.Info(fmt.Sprintf("Harvester Deployment Information:"), nil)
	ctx.Log.Info(fmt.Sprintf("  - Version: %s", version), nil)
	ctx.Log.Info(fmt.Sprintf("  - Boot Script: %s", scriptURL), nil)
	ctx.Log.Info(fmt.Sprintf("  - Nodes: %d VMs configured", harvesterVMCount), nil)
	ctx.Log.Info(fmt.Sprintf("  - VMs will boot from iPXE and auto-configure Harvester cluster"), nil)
	ctx.Log.Info(fmt.Sprintf("  - Access UI at: https://<first-vm-ip>:8443 after boot completes"), nil)
	
	return nil
}
