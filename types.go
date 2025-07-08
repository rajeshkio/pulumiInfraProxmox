package main

import (
	"github.com/muhlba91/pulumi-proxmoxve/sdk/v7/go/proxmoxve/vm"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type Action struct {
	Type      string                 `yaml:"type"`
	DependsOn []string               `yaml:"dependsOn,omitempty"`
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

type IPXEConfig struct {
	BootServerURL string   `yaml:"bootServerUrl"`
	OSType        string   `yaml:"osType"`
	Version       string   `yaml:"version"`
	BaseURL       string   `yaml:"baseUrl,omitempty"`
	ConfigUrl     string   `yaml:"configUrl,omitempty"`
	KernelParams  []string `yaml:"kernelParams,omitempty"`
	AutoInstall   bool     `yaml:"autoInstall,omitempty"`
	ISOFileName   string   `yaml:"isoFileName,omitempty"`

	KernelURL string `yaml:"kernelUrl,omitempty"`
	InitrdURL string `yaml:"initrdUrl,omitempty"`
	ISOURL    string `yaml:"isoUrl,omitempty"`
}
type VMTemplate struct {
	Name        string   `yaml:"name"`
	VMName      string   `yaml:"vmName"`
	ID          int64    `yaml:"id"`
	DiskSize    int64    `yaml:"disksize"`
	Memory      int64    `yaml:"memory"`
	CPU         int64    `yaml:"cpu"`
	IPConfig    string   `yaml:"ipconfig"`
	IPs         []string `yaml:"ips,omitempty"`
	Gateway     string   `yaml:"gateway"`
	Username    string   `yaml:"username"`
	Password    string   `yaml:"password,omitempty"` // global password
	AuthMethod  string   `yaml:"authMethod"`
	Count       int64    `yaml:"count,omitempty"`
	Role        string   `yaml:"role,omitempty"` // NEW!
	Actions     []Action `yaml:"actions,omitempty"`
	ProxmoxNode string   `yaml:"proxmoxNode,omitempty"`

	// ipxe boot options for Harvester
	BootMethod string      `yaml:"bootMethod,omitempty"`
	IPXEConfig *IPXEConfig `yaml:"ipxeConfig,omitempty"`
}

type RoleGroup struct {
	VMs []*vm.VirtualMachine
	IPs []string
}
type ActionHandler func(ctx *pulumi.Context, actionctx ActionContext) error
