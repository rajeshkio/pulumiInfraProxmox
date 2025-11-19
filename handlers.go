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
	"kubeadm":   handleKubeadmService,
	"harvester": handleHarvesterService,
	// "talos":     handleTalosService,
}

func handleK3sService(ctx *pulumi.Context, serviceCtx ServiceContext) error {
	ctx.Log.Info(fmt.Sprintf("Installing K3s service on %d VMs", len(serviceCtx.VMs)), nil)

	err := installK3SLoadBalancer(ctx, serviceCtx)
	if err != nil {
		return fmt.Errorf("failed to install K3s load balancer: %w", err)
	}

	if len(serviceCtx.ServiceConfig.LoadBalancer) == 0 {
		return fmt.Errorf("k3s service requires a load balancer configured")
	}

	lbName := serviceCtx.ServiceConfig.LoadBalancer[0]
	lbKey := lbName + "-ips"
	lbIPs, ok := serviceCtx.GlobalDeps[lbKey].([]string)
	if !ok || len(lbIPs) == 0 {
		return fmt.Errorf("k3s server needs loadbalancer IP but they are not available")
	}

	lbIP := lbIPs[0]
	ctx.Log.Info(fmt.Sprintf("installing k3s server with LBIP: %s", lbIP), nil)

	//var k3sCommands []*remote.Command
	var k3sServerToken pulumi.StringOutput
	var firstServerIP string
	var lastServerCommand pulumi.Resource

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
			lastServerCommand = k3sCmd
			//	k3sCommands = append(k3sCommands, k3sCmd)
			tokenCmd, err := getK3sToken(ctx, serverIP, serviceCtx.VMPassword, k3sCmd)
			if err != nil {
				return fmt.Errorf("cannot get k3s token: %w", err)
			}
			k3sServerToken = tokenCmd.Stdout
		} else {
			k3sCmd, err := installK3SServer(ctx, lbIP, serviceCtx.VMPassword, serverIP, serverVM, false, k3sServerToken, nil)
			if err != nil {
				return fmt.Errorf("cannot install k3s on server %s: %w", serverIP, serverVM)
			}
			lastServerCommand = k3sCmd
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

	workerNodes := serviceCtx.ServiceConfig.Workers
	if len(workerNodes) == 0 {
		ctx.Log.Info("No worker nodes configured for k3s", nil)
		return nil
	}

	// Get worker VMs and IPs
	var workerVMs []*vm.VirtualMachine
	var workerIPs []string
	for _, nodeName := range workerNodes {
		vms, ok := serviceCtx.GlobalDeps[nodeName+"-vms"]
		if !ok {
			ctx.Log.Warn(fmt.Sprintf("Worker VMs for '%s' not found", nodeName), nil)
			continue
		}
		ips, ok := serviceCtx.GlobalDeps[nodeName+"-ips"].([]string)
		if !ok {
			ctx.Log.Warn(fmt.Sprintf("Worker IPs for '%s' not found", nodeName), nil)
			continue
		}
		workerVMs = append(workerVMs, vms.([]*vm.VirtualMachine)...)
		workerIPs = append(workerIPs, ips...)
	}

	if len(workerVMs) == 0 {
		ctx.Log.Info("No worker VMs found to join", nil)
		return nil
	}

	ctx.Log.Info(fmt.Sprintf("Installing k3s agent on %d worker nodes", len(workerVMs)), nil)

	// Install workers - they join the cluster as agents
	for i, workerVM := range workerVMs {
		workerIP := workerIPs[i]
		ctx.Log.Info(fmt.Sprintf("Installing k3s agent on worker %d: %s", i+1, workerIP), nil)

		_, err := installK3SWorker(ctx, lbIP, serviceCtx.VMPassword, workerIP, workerVM, lastServerCommand, k3sServerToken)
		if err != nil {
			return fmt.Errorf("failed to install k3s agent on worker %s: %w", workerIP, err)
		}
	}

	ctx.Log.Info(fmt.Sprintf("k3s agents installed on %d workers", len(workerVMs)), nil)
	return nil
}

func installK3SLoadBalancer(ctx *pulumi.Context, serviceCtx ServiceContext) error {
	if len(serviceCtx.ServiceConfig.LoadBalancer) == 0 {
		ctx.Log.Info("No load balancer configured for K3s, skipping HAProxy installation", nil)
		return nil
	}

	lbName := serviceCtx.ServiceConfig.LoadBalancer[0]
	lbVMs, ok := serviceCtx.GlobalDeps[lbName+"-vms"]
	if !ok {
		return fmt.Errorf("load balancer VMs '%s' not found", lbName)
	}
	lbIps, ok := serviceCtx.GlobalDeps[lbName+"-ips"].([]string)
	if !ok || len(lbIps) == 0 {
		return fmt.Errorf("load balancer IPs '%s' not found", lbName)
	}

	lbVM := lbVMs.([]*vm.VirtualMachine)[0]
	lbIP := lbIps[0]

	backendIPs := serviceCtx.IPs

	ports := serviceCtx.ServiceConfig.Config["ports"]
	if ports == nil {
		return fmt.Errorf("no ports configured for K3s load balancer")
	}
	ctx.Log.Info(fmt.Sprintf("Installing HAProxy on %s for %d K3s backends", lbIP, len(backendIPs)), nil)
	haproxyConfig := generateK3sHAProxyConfig(backendIPs)

	installScript := fmt.Sprintf(`
	echo "Installing HAProxy on load balancer %s"

# Update system (non-interactive)
sudo DEBIAN_FRONTEND=noninteractive apt-get update -y 
sudo DEBIAN_FRONTEND=noninteractive apt-get install -y haproxy

# Backup original config
sudo cp /etc/haproxy/haproxy.cfg /etc/haproxy/haproxy.cfg.bak || true

# Write new configuration
sudo tee /etc/haproxy/haproxy.cfg << 'EOF'
%s
EOF

# Validate configuration
if ! sudo haproxy -f /etc/haproxy/haproxy.cfg -c; then
    echo "HAProxy configuration validation failed!"
    exit 1
fi

# Enable and restart HAProxy
sudo systemctl enable haproxy
sudo systemctl restart haproxy

# Show status
sudo systemctl status haproxy --no-pager

echo "HAProxy installation completed for K3S"
`, lbIP, haproxyConfig)

	_, err := remote.NewCommand(ctx, fmt.Sprintf("haproxy-install-%s", lbIP),
		&remote.CommandArgs{
			Connection: &remote.ConnectionArgs{
				Host:           pulumi.String(lbIP),
				User:           pulumi.String("rajeshk"),
				PrivateKey:     pulumi.String(os.Getenv("PROXMOX_VE_SSH_PRIVATE_KEY")),
				PerDialTimeout: pulumi.IntPtr(30),
				DialErrorLimit: pulumi.IntPtr(20),
			},
			Create:   pulumi.String(installScript),
			Triggers: pulumi.Array{pulumi.String("always-run")}, // Be careful with this in prod, maybe hash the config
		},
		pulumi.DependsOn([]pulumi.Resource{lbVM}),
		pulumi.Timeouts(&pulumi.CustomTimeouts{
			Create: "10m",
		}),
	)
	return err
}

func generateK3sHAProxyConfig(backendIPs []string) string {
	var config strings.Builder
	config.WriteString(`global
    log /dev/log local0
    log /dev/log local1 notice
    chroot /var/lib/haproxy
    stats socket /run/haproxy/admin.sock mode 660 level admin
    stats timeout 30s
    user haproxy
    group haproxy
    daemon

defaults
    log     global
    mode    tcp
    option  tcplog
    option  dontlognull
    timeout connect 5000
    timeout client  50000
    timeout server  50000

listen stats
    bind *:8404
    stats enable
    stats uri /stats
    stats refresh 30s

`)

	// K3s API backend
	config.WriteString(`frontend k3s-api-frontend
    bind *:6443
    mode tcp
    option tcplog
    default_backend k3s-api-backend

backend k3s-api-backend
    mode tcp
    balance roundrobin
    option tcp-check
`)

	for i, ip := range backendIPs {
		config.WriteString(fmt.Sprintf("    server k3s-%d %s:6443 check fall 3 rise 2\n", i+1, ip))
	}

	return config.String()
}
func installK3SServer(ctx *pulumi.Context, lbIP, vmPassword, serverIP string, vmDependency pulumi.Resource, isFirstServer bool, k3sToken pulumi.StringOutput, haproxyDependency pulumi.Resource) (*remote.Command, error) {
	var k3sCommand pulumi.StringInput

	if isFirstServer {
		k3sCommand = pulumi.Sprintf(`
			sudo bash -c "cat > /etc/resolv.conf << 'EOF'
nameserver 192.168.90.1
EOF"
			curl -L https://get.k3s.io | sudo sh -s - server --cluster-init --tls-san=%s --tls-san=$(hostname -I | awk '{print $1}') --write-kubeconfig-mode 644
			sudo systemctl enable --now k3s
			sleep 100
			sudo k3s kubectl wait --for=condition=Ready nodes --all --timeout=300s
			sudo ls /var/lib/rancher/k3s/server/node-token
				`, lbIP)
	} else {
		k3sCommand = pulumi.Sprintf(`
			sudo bash -c "cat > /etc/resolv.conf << 'EOF'
nameserver 192.168.90.1
EOF"
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
			Host:           pulumi.String(serverIP),
			User:           pulumi.String("rajeshk"),
			PrivateKey:     pulumi.String(os.Getenv("PROXMOX_VE_SSH_PRIVATE_KEY")),
			Password:       pulumi.String(vmPassword),
			PerDialTimeout: pulumi.IntPtr(30),
			DialErrorLimit: pulumi.IntPtr(20),
		},
		Create: k3sCommand,
	}, pulumi.DependsOn(dependencies))
	return cmd, err
}

// installK3SWorker installs K3s agent on worker nodes
func installK3SWorker(ctx *pulumi.Context, lbIP, vmPassword, workerIP string, vmDependency pulumi.Resource, serverDependency pulumi.Resource, k3sToken pulumi.StringOutput) (*remote.Command, error) {

	k3sCommand := pulumi.Sprintf(`
		# Set DNS resolver
		sudo tee /etc/resolv.conf << 'EOF'
nameserver 192.168.90.1
EOF

		# Wait for server to be ready
		until curl -k -s https://%s:6443/ping; do
			echo "Waiting for K3s server to be ready..."
			sleep 10
		done

		# Install K3s agent
		curl -sfL https://get.k3s.io | K3S_URL=https://%s:6443 K3S_TOKEN=%s sudo sh -

		echo "K3s agent joined cluster successfully"
	`, lbIP, lbIP, k3sToken)

	resourceName := fmt.Sprintf("k3s-worker-%s", strings.ReplaceAll(workerIP, ".", "-"))
	dependencies := []pulumi.Resource{vmDependency}
	if serverDependency != nil {
		dependencies = append(dependencies, serverDependency)
		ctx.Log.Info(fmt.Sprintf("K3s worker %s will wait for K3s Server installation", workerIP), nil)
	}

	cmd, err := remote.NewCommand(ctx, resourceName, &remote.CommandArgs{
		Connection: &remote.ConnectionArgs{
			Host:           pulumi.String(workerIP),
			User:           pulumi.String("rajeshk"),
			PrivateKey:     pulumi.String(os.Getenv("PROXMOX_VE_SSH_PRIVATE_KEY")),
			Password:       pulumi.String(vmPassword),
			PerDialTimeout: pulumi.IntPtr(30),
			DialErrorLimit: pulumi.IntPtr(20),
		},
		Create: k3sCommand,
	}, pulumi.DependsOn(dependencies))

	return cmd, err
}

func getK3sToken(ctx *pulumi.Context, firstServerIP, vmPassword string, vmDependency pulumi.Resource) (*remote.Command, error) {
	resourceName := fmt.Sprintf("k3s-token-%s", strings.ReplaceAll(firstServerIP, ".", "-"))
	cmd, err := remote.NewCommand(ctx, resourceName, &remote.CommandArgs{
		Connection: &remote.ConnectionArgs{
			Host:           pulumi.String(firstServerIP),
			User:           pulumi.String("rajeshk"),
			PrivateKey:     pulumi.String(os.Getenv("PROXMOX_VE_SSH_PRIVATE_KEY")),
			Password:       pulumi.String(vmPassword),
			PerDialTimeout: pulumi.IntPtr(30),
			DialErrorLimit: pulumi.IntPtr(20),
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

		sudo cat /etc/rancher/k3s/k3s.yaml | sed 's/127.0.0.1:6443/%s:6443/g'`, lbIP) // k3s kubeconfig has frontend port to 6444 as per the

	cmd, err := remote.NewCommand(ctx, resourceName, &remote.CommandArgs{
		Connection: &remote.ConnectionArgs{
			Host:           pulumi.String(serverIP),
			User:           pulumi.String("rajeshk"),
			PrivateKey:     pulumi.String(os.Getenv("PROXMOX_VE_SSH_PRIVATE_KEY")),
			Password:       pulumi.String(vmPassword),
			PerDialTimeout: pulumi.IntPtr(30),
			DialErrorLimit: pulumi.IntPtr(20),
		},
		Create: pulumi.String(kubeconfigCommand),
	}, pulumi.DependsOn([]pulumi.Resource{vmDependency}))

	if err != nil {
		return err
	}

	kubeconfigPath := "./k3s-kubeconfig.yaml"
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

	err := installRKE2LoadBalancer(ctx, serviceCtx)
	if err != nil {
		return fmt.Errorf("failed to install K3s load balancer: %w", err)
	}

	if len(serviceCtx.ServiceConfig.LoadBalancer) == 0 {
		return fmt.Errorf("rke2 server needs loadbalancer IP but they are not available")
	}

	lbName := serviceCtx.ServiceConfig.LoadBalancer[0]
	lbKey := lbName + "-ips"

	lbIPs, ok := serviceCtx.GlobalDeps[lbKey].([]string)
	if !ok || len(lbIPs) == 0 {
		return fmt.Errorf("rke2 server needs loadbalancer IP but they are not available")
	}
	lbIP := lbIPs[0]

	ctx.Log.Info(fmt.Sprintf("Installing RKE2 server with LB IP: %s (from %s)", lbIP, lbName), nil)

	//Install Server (Control Plane)
	controlPlaneNodes := serviceCtx.ServiceConfig.ControlPlane
	if len(controlPlaneNodes) == 0 {
		return fmt.Errorf("rke2 service requires control-plane nodes configured")
	}

	// Get server VMs and IPs
	var serverVMs []*vm.VirtualMachine
	var serverIPs []string
	for _, nodeName := range controlPlaneNodes {
		vms, ok := serviceCtx.GlobalDeps[nodeName+"-vms"]
		if !ok {
			continue
		}
		ips, ok := serviceCtx.GlobalDeps[nodeName+"-ips"].([]string)
		if !ok {
			continue
		}
		serverVMs = append(serverVMs, vms.([]*vm.VirtualMachine)...)
		serverIPs = append(serverIPs, ips...)
	}

	ctx.Log.Info(fmt.Sprintf("installing rke2 server with LBIP: %s", lbIP), nil)
	var rke2ServerToken pulumi.StringOutput
	var firstServerIP string
	var lastServerCommand pulumi.Resource

	// Install servers sequentially
	for i, serverVM := range serverVMs {
		serverIP := serverIPs[i]
		isFirstServer := (i == 0)

		ctx.Log.Info(fmt.Sprintf("Installing RKE2 on server %d: %s", i+1, serverIP), nil)

		if isFirstServer {
			firstServerIP = serverIP
			ctx.Log.Info(fmt.Sprintf("installing rke2 on server %d: %s", i+1, serverIP), nil)

			rke2Cmd, err := installRKE2Server(ctx, lbIP, serviceCtx.VMPassword, serverIP, serverVM, true, pulumi.String("").ToStringOutput(), nil)
			if err != nil {
				return fmt.Errorf("cannot install RKE2 server on first node %s: %w", serverIP, err)
			}
			lastServerCommand = rke2Cmd

			tokenCmd, err := getRKE2Token(ctx, serverIP, serviceCtx.VMPassword, rke2Cmd)
			if err != nil {
				return fmt.Errorf("cannot get rke2 token: %w", err)
			}
			rke2ServerToken = tokenCmd.Stdout
		} else {
			rke2Cmd, err := installRKE2Server(ctx, lbIP, serviceCtx.VMPassword, serverIP, serverVM, false, rke2ServerToken, nil)
			if err != nil {
				return fmt.Errorf("cannot install rke2 on server %s: %w", serverIP, serverVM)
			}
			lastServerCommand = rke2Cmd
		}
	}

	// Export kubeconfig from first server
	if firstServerIP != "" {
		err := getRKE2Kubeconfig(ctx, firstServerIP, serviceCtx.VMPassword, lbIP, serverVMs[0])
		if err != nil {
			return fmt.Errorf("failed to extract rke2 kubeconfig: %w", err)
		}
	}

	ctx.Log.Info(fmt.Sprintf("RKE2 service installed on %d servers", len(serverVMs)), nil)

	// Install Workers (Agents)
	workerNodes := serviceCtx.ServiceConfig.Workers
	if len(workerNodes) == 0 {
		ctx.Log.Info("No worker nodes configured for RKE2", nil)
		return nil
	}

	// Get worker VMs and IPs
	var workerVMs []*vm.VirtualMachine
	var workerIPs []string
	for _, nodeName := range workerNodes {
		vms, ok := serviceCtx.GlobalDeps[nodeName+"-vms"]
		if !ok {
			ctx.Log.Warn(fmt.Sprintf("Worker VMs for '%s' not found", nodeName), nil)
			continue
		}
		ips, ok := serviceCtx.GlobalDeps[nodeName+"-ips"].([]string)
		if !ok {
			ctx.Log.Warn(fmt.Sprintf("Worker IPs for '%s' not found", nodeName), nil)
			continue
		}
		workerVMs = append(workerVMs, vms.([]*vm.VirtualMachine)...)
		workerIPs = append(workerIPs, ips...)
	}

	if len(workerVMs) == 0 {
		ctx.Log.Info("No worker VMs found to join", nil)
		return nil
	}

	ctx.Log.Info(fmt.Sprintf("Installing RKE2 agent on %d worker nodes", len(workerVMs)), nil)

	// Install workers - they join the cluster as agents
	for i, workerVM := range workerVMs {
		workerIP := workerIPs[i]
		ctx.Log.Info(fmt.Sprintf("Installing RKE2 agent on worker %d: %s", i+1, workerIP), nil)

		_, err := installRKE2Worker(ctx, lbIP, serviceCtx.VMPassword, workerIP, workerVM, lastServerCommand, rke2ServerToken)
		if err != nil {
			return fmt.Errorf("failed to install RKE2 agent on worker %s: %w", workerIP, err)
		}
	}

	ctx.Log.Info(fmt.Sprintf("RKE2 agents installed on %d workers", len(workerVMs)), nil)
	return nil
}
func installRKE2LoadBalancer(ctx *pulumi.Context, serviceCtx ServiceContext) error {
	if len(serviceCtx.ServiceConfig.LoadBalancer) == 0 {
		ctx.Log.Info("No load balancer configured for RKE2, skipping HAProxy installation", nil)
		return nil
	}

	lbName := serviceCtx.ServiceConfig.LoadBalancer[0]
	lbVMs, ok := serviceCtx.GlobalDeps[lbName+"-vms"]
	if !ok {
		return fmt.Errorf("load balancer VMs '%s' not found", lbName)
	}

	lbIPs, ok := serviceCtx.GlobalDeps[lbName+"-ips"].([]string)
	if !ok || len(lbIPs) == 0 {
		return fmt.Errorf("load balancer IPs '%s' not found", lbName)
	}

	lbVM := lbVMs.([]*vm.VirtualMachine)[0]
	lbIP := lbIPs[0]

	// Get backend IPs from control plane
	backendIPs := []string{}
	for _, nodeName := range serviceCtx.ServiceConfig.ControlPlane {
		if ips, ok := serviceCtx.GlobalDeps[nodeName+"-ips"].([]string); ok {
			backendIPs = append(backendIPs, ips...)
		}
	}

	ctx.Log.Info(fmt.Sprintf("Installing HAProxy on %s for %d RKE2 backends", lbIP, len(backendIPs)), nil)

	// Generate HAProxy config for RKE2
	haproxyConfig := generateRKE2HAProxyConfig(backendIPs)

	// Install HAProxy
	installScript := fmt.Sprintf(`
echo "Installing HAProxy for RKE2 on %s"

# Update system
sudo DEBIAN_FRONTEND=noninteractive apt-get update -y 
sudo DEBIAN_FRONTEND=noninteractive apt-get install -y haproxy

# Backup original config
sudo cp /etc/haproxy/haproxy.cfg /etc/haproxy/haproxy.cfg.bak || true

# Write new configuration
sudo tee /etc/haproxy/haproxy.cfg << 'EOF'
%s
EOF

# Validate configuration
if ! sudo haproxy -f /etc/haproxy/haproxy.cfg -c; then
    echo "HAProxy configuration validation failed!"
    exit 1
fi

# Enable and restart HAProxy
sudo systemctl enable haproxy
sudo systemctl restart haproxy

echo "HAProxy installation completed for RKE2"
`, lbIP, haproxyConfig)

	_, err := remote.NewCommand(ctx, fmt.Sprintf("rke2-haproxy-%s", lbIP),
		&remote.CommandArgs{
			Connection: &remote.ConnectionArgs{
				Host:           pulumi.String(lbIP),
				User:           pulumi.String("rajeshk"),
				PrivateKey:     pulumi.String(os.Getenv("PROXMOX_VE_SSH_PRIVATE_KEY")),
				PerDialTimeout: pulumi.IntPtr(30),
				DialErrorLimit: pulumi.IntPtr(20),
			},
			Create: pulumi.String(installScript),
		},
		pulumi.DependsOn([]pulumi.Resource{lbVM}),
		pulumi.Timeouts(&pulumi.CustomTimeouts{
			Create: "10m",
		}),
	)

	return err
}
func generateRKE2HAProxyConfig(backendIPs []string) string {
	var config strings.Builder
	config.WriteString(`global
    log /dev/log local0
    log /dev/log local1 notice
    chroot /var/lib/haproxy
    stats socket /run/haproxy/admin.sock mode 660 level admin
    stats timeout 30s
    user haproxy
    group haproxy
    daemon

defaults
    log     global
    mode    tcp
    option  tcplog
    option  dontlognull
    timeout connect 5000
    timeout client  50000
    timeout server  50000

listen stats
    bind *:8404
    stats enable
    stats uri /stats
    stats refresh 30s

`)

	// RKE2 API backend (port 6443)
	config.WriteString(`frontend rke2-api-frontend
    bind *:6443
    mode tcp
    option tcplog
    default_backend rke2-api-backend

backend rke2-api-backend
    mode tcp
    balance roundrobin
    option tcp-check
`)

	for i, ip := range backendIPs {
		config.WriteString(fmt.Sprintf("    server rke2-%d %s:6443 check fall 3 rise 2\n", i+1, ip))
	}

	// RKE2 Supervisor backend (port 9345)
	config.WriteString(`

frontend rke2-supervisor-frontend
    bind *:9345
    mode tcp
    option tcplog
    default_backend rke2-supervisor-backend

backend rke2-supervisor-backend
    mode tcp
    balance roundrobin
    option tcp-check
`)

	for i, ip := range backendIPs {
		config.WriteString(fmt.Sprintf("    server rke2-supervisor-%d %s:9345 check fall 3 rise 2\n", i+1, ip))
	}

	return config.String()
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
			Host:           pulumi.String(serverIP),
			User:           pulumi.String("rajeshk"),
			PrivateKey:     pulumi.String(os.Getenv("PROXMOX_VE_SSH_PRIVATE_KEY")),
			Password:       pulumi.String(vmPassword),
			PerDialTimeout: pulumi.IntPtr(30),
			DialErrorLimit: pulumi.IntPtr(20),
		},
		Create: rke2Command,
	}, pulumi.DependsOn(dependencies))
	return cmd, err
}

func installRKE2Worker(ctx *pulumi.Context, lbIP, vmPassword, workerIP string, vmDependency pulumi.Resource, serverDependency pulumi.Resource, rke2Token pulumi.StringOutput) (*remote.Command, error) {

	rke2Command := pulumi.Sprintf(`
		# Set DNS resolver
		sudo tee /etc/resolv.conf << 'EOF'
nameserver 192.168.90.1
EOF

		# Wait for server to be ready
		until curl -k -s https://%s:9345/ping; do
			echo "Waiting for RKE2 server to be ready..."
			sleep 10
		done

		# Create RKE2 config directory
		sudo mkdir -p /etc/rancher/rke2

		# Create RKE2 agent configuration
		sudo tee /etc/rancher/rke2/config.yaml << 'EOF'
server: https://%s:9345
token: %s
EOF

		# Download and install RKE2
		curl -sfL https://get.rke2.io | INSTALL_RKE2_TYPE="agent" sudo sh -

		# Enable and start RKE2 agent
		sudo systemctl enable rke2-agent.service
		sudo systemctl start rke2-agent.service

		echo "RKE2 agent joined cluster successfully"
	`, lbIP, lbIP, rke2Token)

	resourceName := fmt.Sprintf("rke2-worker-%s", strings.ReplaceAll(workerIP, ".", "-"))
	dependencies := []pulumi.Resource{vmDependency}
	if serverDependency != nil {
		dependencies = append(dependencies, serverDependency)
		ctx.Log.Info(fmt.Sprintf("RKE2 worker %s will wait for RKE2 Server installation", workerIP), nil)
	}

	cmd, err := remote.NewCommand(ctx, resourceName, &remote.CommandArgs{
		Connection: &remote.ConnectionArgs{
			Host:           pulumi.String(workerIP),
			User:           pulumi.String("rajeshk"),
			PrivateKey:     pulumi.String(os.Getenv("PROXMOX_VE_SSH_PRIVATE_KEY")),
			Password:       pulumi.String(vmPassword),
			PerDialTimeout: pulumi.IntPtr(30),
			DialErrorLimit: pulumi.IntPtr(20),
		},
		Create: rke2Command,
	}, pulumi.DependsOn(dependencies))

	return cmd, err
}

func getRKE2Token(ctx *pulumi.Context, firstServerIP, vmPassword string, vmDependency pulumi.Resource) (*remote.Command, error) {
	resourceName := fmt.Sprintf("rke2-token-%s", strings.ReplaceAll(firstServerIP, ".", "-"))
	cmd, err := remote.NewCommand(ctx, resourceName, &remote.CommandArgs{
		Connection: &remote.ConnectionArgs{
			Host:           pulumi.String(firstServerIP),
			User:           pulumi.String("rajeshk"),
			PrivateKey:     pulumi.String(os.Getenv("PROXMOX_VE_SSH_PRIVATE_KEY")),
			Password:       pulumi.String(vmPassword),
			PerDialTimeout: pulumi.IntPtr(30),
			DialErrorLimit: pulumi.IntPtr(20),
		},
		Create: pulumi.String(`
			# Wait for RKE2 to be fully ready and token file to exist
			while ! sudo test -f /var/lib/rancher/rke2/server/node-token; do
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
			Host:           pulumi.String(serverIP),
			User:           pulumi.String("rajeshk"),
			PrivateKey:     pulumi.String(os.Getenv("PROXMOX_VE_SSH_PRIVATE_KEY")),
			Password:       pulumi.String(vmPassword),
			PerDialTimeout: pulumi.IntPtr(30),
			DialErrorLimit: pulumi.IntPtr(20),
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
	ctx.Log.Info("Installing Kubeadm Kubernetes cluster", nil)

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

// K3S Worker Installation Function
// ========================================

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
		Host:           pulumi.String(ip),
		User:           pulumi.String("rajeshk"),
		PrivateKey:     pulumi.String(os.Getenv("PROXMOX_VE_SSH_PRIVATE_KEY")),
		PerDialTimeout: pulumi.IntPtr(30),
		DialErrorLimit: pulumi.IntPtr(20),
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
		Host:           pulumi.String(ip),
		User:           pulumi.String("rajeshk"),
		PrivateKey:     pulumi.String(os.Getenv("PROXMOX_VE_SSH_PRIVATE_KEY")),
		PerDialTimeout: pulumi.IntPtr(30),
		DialErrorLimit: pulumi.IntPtr(20),
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
	bootServerURL := getConfigString(config, "boot-server-url", "http://192.168.90.103:8080")

	// Check if there are any Harvester VMs configured
	var harvesterVMs []*vm.VirtualMachine

	for _, targetName := range targets {
		if vms, ok := serviceCtx.GlobalDeps[targetName+"-vms"]; ok {
			vmList := vms.([]*vm.VirtualMachine)
			harvesterVMs = append(harvesterVMs, vmList...)
		}
	}

	if len(harvesterVMs) == 0 {
		ctx.Log.Info("Harvester service enabled but no VMs with bootMethod: ipxe found - skipping", nil)
		ctx.Log.Info("Note: Harvester VMs auto-configure via iPXE boot - no post-deployment needed", nil)
		return nil
	}

	nodeCount := len(harvesterVMs)
	if nodeCount == 2 {
		return fmt.Errorf("CRITICAL: Harvester cannot run with 2 nodes - this breaks etcd quorum. Use 1 node (no HA) or 3+ nodes (with HA)")
	}

	if nodeCount > 1 && nodeCount%2 == 0 {
		ctx.Log.Warn(fmt.Sprintf("WARNING: Even number of nodes (%d) is not recommended. Use odd numbers (1, 3, 5, 7) for proper etcd quorum", nodeCount), nil)
		ctx.Log.Warn("Consider adjusting to 3, 5, or 7 nodes for optimal HA", nil)
	}

	ctx.Log.Info("=== Harvester HCI Cluster Deployment Plan ===", nil)
	ctx.Log.Info(fmt.Sprintf("  Version: %s", version), nil)
	ctx.Log.Info(fmt.Sprintf("  Boot Server: %s", bootServerURL), nil)
	ctx.Log.Info(fmt.Sprintf("  Total Management Nodes: %d", nodeCount), nil)
	ctx.Log.Info("  IP Assignment: DHCP (all nodes)", nil)

	switch nodeCount {
	case 1:
		ctx.Log.Info("  Mode: Single Node (No HA)", nil)
	case 3:
		ctx.Log.Info("  Mode: 3-Node HA (can tolerate 1 failure)", nil)
	case 5:
		ctx.Log.Info("  Mode: 5-Node HA (can tolerate 2 failures)", nil)
	case 7:
		ctx.Log.Info("  Mode: 7-Node HA (can tolerate 3 failures)", nil)
	}
	// Log configuration info
	ctx.Log.Info("", nil)
	ctx.Log.Info("Boot Sequence:", nil)
	for i := range nodeCount {
		if i == 0 {
			ctx.Log.Info("  Node 1: harvester-create.iso → creates cluster", nil)
		} else {
			ctx.Log.Info(fmt.Sprintf("  Node %d: harvester-join.iso → joins cluster", i+1), nil)
		}
	}

	ctx.Log.Info("", nil)
	ctx.Log.Info("Configuration Files on Boot Server:", nil)
	ctx.Log.Info(fmt.Sprintf("  Create: %s/versions/%s/harvester-create-config.yaml", bootServerURL, version), nil)
	ctx.Log.Info(fmt.Sprintf("  Join:   %s/versions/%s/harvester-join-config.yaml", bootServerURL, version), nil)
	ctx.Log.Info("  Note: VIP, token, and network settings are defined in these configs", nil)

	// Export only the basics
	ctx.Export("harvester-node-count", pulumi.Int(nodeCount))
	ctx.Export("harvester-boot-server", pulumi.String(bootServerURL))
	ctx.Export("harvester-version", pulumi.String(version))

	ctx.Log.Info("", nil)
	ctx.Log.Info("Post-Installation:", nil)
	ctx.Log.Info("  • Access UI at VIP defined in config (port 8443)", nil)
	ctx.Log.Info("  • SSH via: ssh rancher@<node-dhcp-ip>", nil)
	ctx.Log.Info("  • Installation takes ~10-15 minutes per node", nil)
	ctx.Log.Info("  • Monitor progress via Proxmox console", nil)

	return nil
}
