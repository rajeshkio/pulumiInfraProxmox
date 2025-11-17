package main

import (
	"github.com/muhlba91/pulumi-proxmoxve/sdk/v7/go/proxmoxve"
	"github.com/muhlba91/pulumi-proxmoxve/sdk/v7/go/proxmoxve/vm"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type ServiceContext struct {
	ServiceName   string
	VMs           []*vm.VirtualMachine
	IPs           []string
	GlobalDeps    map[string]interface{}
	Config        map[string]interface{}
	VMPassword    string
	ServiceConfig *ServiceConfig
}

type IPXEConfig struct {
	BootServerURL string   `yaml:"bootServerUrl"`
	OSType        string   `yaml:"osType"`
	Version       string   `yaml:"version"`
	BaseURL       string   `yaml:"baseUrl,omitempty"`
	ConfigUrl     string   `yaml:"configUrl,omitempty"`
	KernelParams  []string `yaml:"kernelParams,omitempty"`
	AutoInstall   bool     `yaml:"autoInstall,omitempty"`
	ISOFiles      []string `yaml:"isoFiles,omitempty"`

	KernelURL string `yaml:"kernelUrl,omitempty"`
	InitrdURL string `yaml:"initrdUrl,omitempty"`
	ISOURL    string `yaml:"isoUrl,omitempty"`
}

type VM struct {
	Name        string      `yaml:"name"`
	Count       int64       `yaml:"count"`
	TemplateID  int64       `yaml:"templateId"`
	Memory      int64       `yaml:"memory"`
	CPU         int64       `yaml:"cpu"`
	DiskSize    int64       `yaml:"diskSize"`
	IPs         []string    `yaml:"ips,omitempty"`
	IPConfig    string      `yaml:"ipconfig,omitempty"`
	Gateway     string      `yaml:"gateway,omitempty"`
	Username    string      `yaml:"username,omitempty"`
	AuthMethod  string      `yaml:"authMethod,omitempty"`
	Password    string      `yaml:"password,omitempty"`
	ProxmoxNode string      `yaml:"proxmoxNode,omitempty"`
	BootMethod  string      `yaml:"bootMethod,omitempty"`
	IPXEConfig  *IPXEConfig `yaml:"ipxeConfig,omitempty"`
	//VMName      string      `yaml:"vmName"`
}

type ServiceConfig struct {
	Enabled          bool                   `yaml:"enabled"`
	Targets          []string               `yaml:"targets,omitempty"`          // VM groups this service runs on
	ControlPlane     []string               `yaml:"control-plane,omitempty"`    // For k8s control plane nodes
	Workers          []string               `yaml:"workers,omitempty"`          // For k8s worker nodes
	LoadBalancer     []string               `yaml:"loadbalancer,omitempty"`     // For load balancer nodes
	BackendDiscovery string                 `yaml:"backendDiscovery,omitempty"` // Which VM group provides backends
	Config           map[string]interface{} `yaml:"config,omitempty"`           // Service-specific config
}

type Services struct {
	K3s       *ServiceConfig `yaml:"k3s,omitempty"`
	Kubeadm   *ServiceConfig `yaml:"kubeadm,omitempty"`
	RKE2      *ServiceConfig `yaml:"rke2,omitempty"`
	HAProxy   *ServiceConfig `yaml:"haproxy,omitempty"`
	Talos     *ServiceConfig `yaml:"talos,omitempty"`
	Harvester *ServiceConfig `yaml:"harvester,omitempty"`
}

type VMGroup struct {
	VMs []*vm.VirtualMachine
	IPs []string
}

type VMCreationConfig struct {
	BatchSize  int `yaml:"batchSize"`  // How many VMs to create in parallel (default: 3)
	MaxRetries int `yaml:"maxRetries"` // How many times to retry failed clones (default: 5)
	BatchDelay int `yaml:"batchDelay"` // Seconds to wait between batches (default: 10)
}

type VMRequest struct {
	VMDef    VM
	Index    int64
	Provider *proxmoxve.Provider
	NodeName string
	Gateway  string
	Password string
}

type HAProxyBackend struct {
	Name         string
	IPs          []string
	FrontendPort int
	BackendPort  int
}

type HAProxyServiceConfig struct {
	APIPort        int            `json:"apiPort"`
	SupervisorPort int            `json:"supervisorPort,omitempty"`
	DashboardPort  int            `json:"dashboardPort,omitempty"`
	ExtraPorts     map[string]int `json:"extraPorts,omitempty"`
}
type ServiceHandler func(ctx *pulumi.Context, serviceCtx ServiceContext) error
