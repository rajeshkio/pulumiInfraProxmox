package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: go run main.go generate <harvester version>")
		os.Exit(1)
	}
	command := os.Args[1]
	version := os.Args[2]

	if command != "generate" {
		fmt.Println("only generate command is supported for now")
		os.Exit(1)
	}

	err := generateAssets(version)
	if err != nil {
		fmt.Println("error")
		os.Exit(1)
	}
}

func generateAssets(version string) error {
	fmt.Printf("generating assets for version %s\n", version)

	bootDir := "boot"
	configDir := "config"

	if err := os.MkdirAll(bootDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %v", err)
	}

	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %v", err)
	}

	err := generateBootScript(version, bootDir)
	if err != nil {
		return fmt.Errorf("failed to generate boot script: %v", err)
	}

	err = generateConfigFile(version, configDir)
	if err != nil {
		return fmt.Errorf("failed to generate config script: %v", err)
	}

	fmt.Printf("created: %s/harvester-%s.ipxe\n", bootDir, version)
	fmt.Printf("created %s/harvester-%s-config.yaml\n", configDir, version)
	return nil
}

func generateBootScript(version, bootDir string) error {
	template := `#!ipxe
set version ` + version + `
set localbase https://ipxe-server.rajesh-kumar.in/iso/harvester/${version}
set rancherbase https://releases.rancher.com/harvester/${version}

dhcp
echo Booting Harvester ${version}

# Try local first, fallback to remote
kernel ${localbase}/harvester-${version}-vmlinuz-amd64 initrd=harvester-${version}-initrd-amd64 ip=dhcp net.ifnames=1 rd.cos.disable rd.noverifyssl root=live:${localbase}/harvester-${version}-rootfs-amd64.squashfs harvester.install.config_url=https://ipxe-server.rajesh-kumar.in/config/harvester-${version}-config.yaml harvester.install.automatic=true || goto rancher_fallback
initrd ${localbase}/harvester-${version}-initrd-amd64 || goto rancher_fallback
goto boot

:rancher_fallback
kernel ${rancherbase}/harvester-${version}-vmlinuz-amd64 initrd=harvester-${version}-initrd-amd64 ip=dhcp net.ifnames=1 rd.cos.disable rd.noverifyssl root=live:${rancherbase}/harvester-${version}-rootfs-amd64.squashfs harvester.install.config_url=https://ipxe-server.rajesh-kumar.in/config/harvester-${version}-config.yaml harvester.install.automatic=true
initrd ${rancherbase}/harvester-${version}-initrd-amd64

:boot
boot
`

	filename := filepath.Join(bootDir, fmt.Sprintf("harvester-%s.ipxe", version))
	return os.WriteFile(filename, []byte(template), 0644)
}

func generateConfigFile(version, configDir string) error {
	template := `#cloud-config
scheme_version: 1
server_url: https://192.168.90.210:443
token: harvester-cluster-` + version + `
os:
  ssh_authorized_keys:
  - "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIINH6/EGN6YmF3lOlXzBG/oPAo0dGpzlnky3RMQAMv5c rajeshkumar@Rajeshs-MacBook-Pro.local"
  password: "YourSecurePassword123"
  hostname: harvester-` + version + `-main
install:
  mode: create
  management_interface:
    interfaces:
    - name: ens18
    default_route: true
    method: dhcp
  device: /dev/sda
  iso_url: https://releases.rancher.com/harvester/` + version + `/harvester-` + version + `-amd64.iso
  vip: 192.168.90.210
  vip_mode: static
  data_disk: /dev/sdb
`

	filename := filepath.Join(configDir, fmt.Sprintf("harvester-%s-config.yaml", version))
	return os.WriteFile(filename, []byte(template), 0644)
}
