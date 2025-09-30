package main

import (
	"fmt"

	"github.com/muhlba91/pulumi-proxmoxve/sdk/v7/go/proxmoxve/vm"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func executeService(ctx *pulumi.Context, serviceName string, config *ServiceConfig, vmGroups map[string][]*vm.VirtualMachine, globalDeps map[string]interface{}, vmPassword string) error {
	handler, exists := serviceHandlers[serviceName]
	if !exists {
		return fmt.Errorf("unknown service: %s", serviceName)
	}

	serviceCtx := ServiceContext{
		ServiceName:   serviceName,
		GlobalDeps:    globalDeps,
		Config:        config.Config,
		VMPassword:    vmPassword,
		ServiceConfig: config,
	}

	allTargets := []string{}
	allTargets = append(allTargets, config.Targets...)
	allTargets = append(allTargets, config.ControlPlane...)
	allTargets = append(allTargets, config.Workers...)
	allTargets = append(allTargets, config.LoadBalancer...)

	for _, target := range allTargets {
		if vms, exists := vmGroups[target]; exists {
			serviceCtx.VMs = append(serviceCtx.VMs, vms...)
			if ips, exists := globalDeps[target+"-ips"].([]string); exists {
				serviceCtx.IPs = append(serviceCtx.IPs, ips...)
			}
		}
	}
	if len(serviceCtx.VMs) == 0 {
		return fmt.Errorf("service %s has no target VMs configured", serviceName)
	}
	ctx.Log.Info(fmt.Sprintf("Executing service '%s' on %d VMs", serviceName, len(serviceCtx.VMs)), nil)
	return handler(ctx, serviceCtx)
}
func executeServices(ctx *pulumi.Context, services *Services, vmGroups map[string][]*vm.VirtualMachine, globalDeps map[string]interface{}, vmPassword string) error {
	if services.HAProxy != nil && services.HAProxy.Enabled {
		err := executeService(ctx, "haproxy", services.HAProxy, vmGroups, globalDeps, vmPassword)
		if err != nil {
			return fmt.Errorf("failed to execute HAProxy service: %w", err)
		}
	}
	if services.K3s != nil && services.K3s.Enabled {
		err := executeService(ctx, "k3s", services.K3s, vmGroups, globalDeps, vmPassword)
		if err != nil {
			return fmt.Errorf("failed to execute K3s service: %w", err)
		}
	}
	if services.RKE2 != nil && services.RKE2.Enabled {
		err := executeService(ctx, "rke2", services.RKE2, vmGroups, globalDeps, vmPassword)
		if err != nil {
			return fmt.Errorf("failed to execute RKE2 service: %w", err)
		}
	}
	return nil
}
