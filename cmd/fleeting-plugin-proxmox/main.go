package main

import (
	proxmox "github.com/myplixa/pve-fleeting-plugin/cmd/fleeting-plugin-proxmox/plugin"
	"gitlab.com/gitlab-org/fleeting/fleeting/plugin"
)

func main() {
	plugin.Main(&proxmox.InstanceGroup{}, proxmox.Version)
}
