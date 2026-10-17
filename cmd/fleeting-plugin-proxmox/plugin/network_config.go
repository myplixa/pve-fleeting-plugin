package plugin

import (
	"context"
	"strconv"
	"strings"

	"github.com/luthermonson/go-proxmox"
)

const (
	net0Device      = "net0"
	ipconfig0Device = "ipconfig0"
	dhcpIPConfig    = "ip=dhcp,ip6=dhcp"
)

// configureInstanceNetwork applies instance_network_bridge / instance_network_vlan_tag
// / instance_network_dhcp settings to a freshly cloned instance's net0
// device, before it is started. A no-op if none of them are configured,
// leaving the template's network config untouched.
func (ig *InstanceGroup) configureInstanceNetwork(ctx context.Context, vm *proxmox.VirtualMachine) error {
	requestedOverride := ig.Settings.InstanceNetworkBridge != "" || ig.Settings.InstanceNetworkVlanTag != nil || ig.Settings.InstanceNetworkDHCP

	if vm.VirtualMachineConfig == nil {
		if requestedOverride {
			ig.log.Warn("network/cloud-init configuration was requested but instance has no VM config to apply it to", "vmid", vm.VMID)
		}

		return nil
	}

	var options []proxmox.VirtualMachineOption

	current := vm.VirtualMachineConfig.Nets[net0Device]

	newNet0, changed := buildNetworkOverride(current, ig.Settings.InstanceNetworkBridge, ig.Settings.InstanceNetworkVlanTag)
	if !changed && (ig.Settings.InstanceNetworkBridge != "" || ig.Settings.InstanceNetworkVlanTag != nil) && current == "" {
		ig.log.Warn("network override was requested but template has no net0 device to apply it to", "vmid", vm.VMID)
	}

	if changed {
		options = append(options, proxmox.VirtualMachineOption{Name: net0Device, Value: newNet0})
	}

	if ig.Settings.InstanceNetworkDHCP {
		options = append(options, proxmox.VirtualMachineOption{Name: ipconfig0Device, Value: dhcpIPConfig})
	}

	return ig.applyVMConfig(ctx, vm, "network", options...)
}

// buildNetworkOverride merges instance_network_bridge / instance_network_vlan_tag
// settings into an existing Proxmox net0-style config string (e.g.
// "virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr0"), preserving the model/MAC and any
// other keys (firewall, rate, mtu, ...) the clone already has. Returns the
// merged string and whether it differs from current.
func buildNetworkOverride(current, bridge string, vlanTag *int) (string, bool) {
	if bridge == "" && vlanTag == nil {
		return current, false
	}

	parts := strings.Split(current, ",")
	if len(parts) == 0 || parts[0] == "" {
		return current, false
	}

	model := parts[0]

	kv := make(map[string]string, len(parts)-1)
	order := make([]string, 0, len(parts)-1)

	for _, part := range parts[1:] {
		key, value, found := strings.Cut(part, "=")
		if !found {
			continue
		}

		kv[key] = value
		order = append(order, key)
	}

	if bridge != "" {
		if _, ok := kv["bridge"]; !ok {
			order = append(order, "bridge")
		}

		kv["bridge"] = bridge
	}

	if vlanTag != nil {
		if *vlanTag > 0 {
			if _, ok := kv["tag"]; !ok {
				order = append(order, "tag")
			}

			kv["tag"] = strconv.Itoa(*vlanTag)
		} else {
			delete(kv, "tag")

			filtered := make([]string, 0, len(order))

			for _, key := range order {
				if key != "tag" {
					filtered = append(filtered, key)
				}
			}

			order = filtered
		}
	}

	result := make([]string, 0, len(order)+1)
	result = append(result, model)

	for _, key := range order {
		result = append(result, key+"="+kv[key])
	}

	merged := strings.Join(result, ",")

	return merged, merged != current
}
