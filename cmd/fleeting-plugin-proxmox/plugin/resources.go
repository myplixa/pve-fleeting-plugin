package plugin

import (
	"context"
	"fmt"

	"github.com/luthermonson/go-proxmox"
)

// configureInstanceResources applies instance_num_cpus / instance_memory_gb /
// instance_disks overrides to a freshly cloned instance, before it is
// started. A no-op for any setting left unset, leaving the template's value
// untouched.
func (ig *InstanceGroup) configureInstanceResources(ctx context.Context, vm *proxmox.VirtualMachine) error {
	if err := ig.configureCPUAndMemory(ctx, vm); err != nil {
		return err
	}

	return ig.configureInstanceDisks(ctx, vm)
}

// gbToMB converts instance_memory_gb to the MB value the Proxmox "memory"
// option expects.
const gbToMB = 1024

// intOption builds a VirtualMachineOption for name if v is set and positive,
// collapsing the "only apply this override if the pointer is non-nil and
// > 0" check that instance_num_cpus/instance_memory_gb/future similar
// settings all share.
func intOption(name string, v *int) (proxmox.VirtualMachineOption, bool) {
	if v == nil || *v <= 0 {
		return proxmox.VirtualMachineOption{}, false
	}

	return proxmox.VirtualMachineOption{Name: name, Value: *v}, true
}

func (ig *InstanceGroup) configureCPUAndMemory(ctx context.Context, vm *proxmox.VirtualMachine) error {
	var options []proxmox.VirtualMachineOption

	if option, ok := intOption("cores", ig.Settings.InstanceNumCPUs); ok {
		options = append(options, option)
	}

	if ig.Settings.InstanceMemoryGB != nil && *ig.Settings.InstanceMemoryGB > 0 {
		options = append(options, proxmox.VirtualMachineOption{Name: "memory", Value: *ig.Settings.InstanceMemoryGB * gbToMB})
	}

	return ig.applyVMConfig(ctx, vm, "CPU/memory", options...)
}

// configureInstanceDisks applies instance_disks: a disk key already present
// on the clone (inherited from the template) is resized via ResizeDisk; a
// disk key that doesn't exist yet is allocated fresh via Config, using
// Proxmox's "<storage>:<size_gb>" new-disk syntax, on that disk's own
// Storage (falling back to Settings.Storage when unset).
func (ig *InstanceGroup) configureInstanceDisks(ctx context.Context, vm *proxmox.VirtualMachine) error {
	if len(ig.Settings.InstanceDisks) == 0 {
		return nil
	}

	existingDisks := map[string]bool{}
	if vm.VirtualMachineConfig != nil {
		for name := range vm.VirtualMachineConfig.MergeDisks() {
			existingDisks[name] = true
		}
	}

	var newDiskOptions []proxmox.VirtualMachineOption

	for _, disk := range ig.Settings.InstanceDisks {
		if existingDisks[disk.Disk] {
			continue
		}

		storage := disk.Storage
		if storage == "" {
			storage = ig.Settings.Storage
		}

		newDiskOptions = append(newDiskOptions, proxmox.VirtualMachineOption{
			Name:  disk.Disk,
			Value: fmt.Sprintf("%s:%d", storage, disk.SizeGB),
		})
	}

	if err := ig.applyVMConfig(ctx, vm, "disk(s)", newDiskOptions...); err != nil {
		return err
	}

	for _, disk := range ig.Settings.InstanceDisks {
		if !existingDisks[disk.Disk] {
			continue
		}

		task, err := vm.ResizeDisk(ctx, disk.Disk, fmt.Sprintf("%dG", disk.SizeGB))
		if err := waitForTask(ctx, task, err, proxmoxTaskWaitTimeout); err != nil {
			return fmt.Errorf("failed to resize instance disk='%s': %w", disk.Disk, err)
		}
	}

	return nil
}
