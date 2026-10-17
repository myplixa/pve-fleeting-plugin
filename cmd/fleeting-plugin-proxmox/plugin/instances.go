package plugin

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/luthermonson/go-proxmox"
	"gitlab.com/gitlab-org/fleeting/fleeting/provider"
	"golang.org/x/sync/errgroup"
)

const (
	proxmoxTaskWaitInterval  = 10 * time.Second
	proxmoxTaskWaitTimeout   = 5 * time.Minute
	proxmoxAgentStartTimeout = 2 * time.Minute
)

var ErrCloneVMWithoutConfiguredStorage = errors.New("attempted to clone a VM without configured storage")

func (ig *InstanceGroup) deployInstance(ctx context.Context, template *proxmox.VirtualMachine, cloneMu *sync.Mutex) (int, error) {
	// Held only around the clone call itself (not the rest of the deploy):
	// a freshly-cloned VM can briefly report its old (pool-listing-cached)
	// name under a recycled VMID right after cloning, and this mutex keeps
	// Update/Decrease/the collector from reading the pool listing while
	// that's in flight. See the field doc on instanceCloningMu.
	ig.instanceCloningMu.Lock()
	VMID, task, err := ig.cloneTemplate(ctx, template, cloneMu)
	ig.instanceCloningMu.Unlock()

	if err != nil {
		return VMID, fmt.Errorf("failed to deploy instance: %w", err)
	}

	ig.log.Info("Deploying new instance", "vmid", VMID)

	var vm *proxmox.VirtualMachine

	// From here on, Proxmox has accepted (and may already have run) the
	// clone, so the target VM may already exist even if a step below
	// fails. Every failure in this closure must fall through to the
	// rename-to-removing tail below instead of returning early - otherwise
	// the VM is orphaned with its -creating name forever (the collector
	// only cleans up -removing instances).
	err = func() error {
		if err := waitForTask(ctx, task, nil, proxmoxTaskWaitTimeout); err != nil {
			return fmt.Errorf("failed to wait for clone task: %w", err)
		}

		// The clone always lands on the template's own node, so look the
		// VM up directly on that node instead of going through the pool
		// listing, which can lag behind a just-finished clone.
		vm, err = ig.getProxmoxVMOnNode(ctx, VMID, template.Node)
		if err != nil {
			return fmt.Errorf("failed to find newly deployed instance vmid='%d': %w", VMID, err)
		}

		if err := ig.configureInstanceNetwork(ctx, vm); err != nil {
			return err
		}

		if err := ig.configureInstanceResources(ctx, vm); err != nil {
			return err
		}

		if err := ig.configureCloudInitUser(ctx, vm); err != nil {
			return err
		}

		// Start the VM
		task, err := vm.Start(ctx)
		if err := waitForTask(ctx, task, err, proxmoxTaskWaitTimeout); err != nil {
			return fmt.Errorf("failed to start newly deployed instance: %w", err)
		}

		// Wait for agent to start
		if err := vm.WaitForAgent(ctx, int(proxmoxAgentStartTimeout/time.Second)); err != nil {
			return fmt.Errorf("failed when waiting for qemu agent to start on newly deployed instance: %w", err)
		}

		return nil
	}()

	if vm == nil {
		// The clone task itself failed, or the lookup right after it did -
		// in both cases we never got a VM handle to rename inside the
		// closure above. Try once more by node before giving up: this is
		// the same failure this fix targets (a VM that exists on Proxmox
		// but couldn't be addressed), so best-effort attempt to mark it for
		// removal rather than leaving it orphaned in -creating.
		var lookupErr error

		vm, lookupErr = ig.getProxmoxVMOnNode(ctx, VMID, template.Node)
		if lookupErr != nil {
			ig.log.Error("failed to find instance to mark for removal after deploy failure", "vmid", VMID, "err", lookupErr)
			return VMID, fmt.Errorf("failed to deploy instance: %w", err)
		}
	}

	newBase := ig.Settings.InstanceNameRunning

	if err != nil {
		ig.log.Error("instance deployment failed, marking for removal", "vmid", VMID, "err", err)
		newBase = ig.Settings.InstanceNameRemoving
	}

	renameTask, renameErr := vm.Config(ctx, proxmox.VirtualMachineOption{
		Name:  "name",
		Value: renameToState(vm.Name, newBase),
	})
	renameErr = waitForTask(ctx, renameTask, renameErr, proxmoxTaskWaitTimeout)

	if renameErr != nil {
		ig.log.Error("failed to rename instance", "vmid", VMID, "err", renameErr)
	}

	if err != nil {
		return VMID, fmt.Errorf("failed to configure instance, marked for removal due to: %w", err)
	}

	return VMID, nil
}

func (ig *InstanceGroup) cloneTemplate(ctx context.Context, template *proxmox.VirtualMachine, cloneMu *sync.Mutex) (int, *proxmox.Task, error) {
	cloneOptions, err := ig.getTemplateCloneOptions(template)
	if err != nil {
		return -1, nil, err
	}

	cloneMu.Lock()
	defer cloneMu.Unlock()

	VMID, task, err := template.Clone(ctx, cloneOptions)
	if err != nil {
		return -1, nil, fmt.Errorf("failed to clone the template: %w", err)
	}

	return VMID, task, nil
}

func (ig *InstanceGroup) getTemplateCloneOptions(template *proxmox.VirtualMachine) (*proxmox.VirtualMachineCloneOptions, error) {
	cloneOptions := &proxmox.VirtualMachineCloneOptions{
		Name:    newInstanceName(ig.Settings.InstanceNameCreating),
		Pool:    ig.Settings.Pool,
		Storage: ig.Settings.Storage,
		Full:    proxmox.IntOrBool(true),
	}

	if !template.Template && ig.Settings.Storage == "" {
		return nil, ErrCloneVMWithoutConfiguredStorage
	}

	if template.Template && ig.Settings.Storage == "" {
		cloneOptions.Full = proxmox.IntOrBool(false)
	}

	return cloneOptions, nil
}

// markStaleInstancesForRemoval sweeps -creating instances left over from a
// previous process' lifetime (it died before the deploy could finish) for
// removal. When alsoSweepRunning is true (the process just self-generated a
// fresh SSH key - see Init), -running instances are swept too: their
// authorized_keys entry was populated at clone time with the *previous*
// process' key, which the new key won't match, so they'd otherwise fail SSH
// auth for every job until torn down for unrelated reasons.
func (ig *InstanceGroup) markStaleInstancesForRemoval(ctx context.Context, alsoSweepRunning bool) error {
	instances, err := ig.listInstances(ctx)
	if err != nil {
		return err
	}

	instancesToMarkForRemoval := make([]*proxmox.ClusterResource, 0, len(instances))

	for _, member := range instances {
		isStale := hasInstanceNamePrefix(member.Name, ig.Settings.InstanceNameCreating) ||
			(alsoSweepRunning && hasInstanceNamePrefix(member.Name, ig.Settings.InstanceNameRunning))

		if !isStale {
			continue
		}

		ig.log.Info("Found stale instance, marking for removal", "name", member.Name, "vmid", member.VMID, "node", member.Node)
		instancesToMarkForRemoval = append(instancesToMarkForRemoval, &member)
	}

	if len(instancesToMarkForRemoval) < 1 {
		return nil
	}

	if err := ig.markInstancesForRemoval(ctx, instancesToMarkForRemoval...); err != nil {
		return fmt.Errorf("failed to mark stale instances for removal: %w", err)
	}

	return nil
}

func (ig *InstanceGroup) markInstancesForRemoval(ctx context.Context, instances ...*proxmox.ClusterResource) error {
	var errorGroup errgroup.Group

	// Trigger the collector exactly once, on the way out, regardless of
	// whether marking succeeded or failed - this also protects a future
	// early return from silently skipping the trigger.
	defer func() { ig.instanceCollectionTrigger <- struct{}{} }()

	for _, instance := range instances {
		errorGroup.Go(func() error {
			log := ig.log.With("name", instance.Name, "vmid", instance.VMID, "node", instance.Node)

			vm, err := ig.getProxmoxVMOnNode(ctx, int(instance.VMID), instance.Node)
			if err != nil {
				log.Error("Failed to mark instance for removal", "err", err)
				return fmt.Errorf("failed to mark instance for removal: %w", err)
			}

			task, err := vm.Config(ctx, proxmox.VirtualMachineOption{
				Name:  "name",
				Value: renameToState(instance.Name, ig.Settings.InstanceNameRemoving),
			})

			if err := waitForTask(ctx, task, err, proxmoxTaskWaitTimeout); err != nil {
				log.Error("Failed to mark instance for removal", "err", err)
				return fmt.Errorf("failed to mark instance for removal: %w", err)
			}

			return nil
		})
	}

	if err := errorGroup.Wait(); err != nil {
		return fmt.Errorf("failed to mark one or more instances for removal: %w", err)
	}

	return nil
}

func (ig *InstanceGroup) isProxmoxResourceAnInstance(member proxmox.ClusterResource) bool {
	return member.Type == "qemu" && member.VMID != uint64(*ig.Settings.TemplateID)
}

// listInstances fetches the pool listing and filters it down to this
// instance group's own instances (see isProxmoxResourceAnInstance),
// excluding the template itself and any unrelated VM sharing the pool.
func (ig *InstanceGroup) listInstances(ctx context.Context) ([]proxmox.ClusterResource, error) {
	pool, err := ig.getProxmoxPool(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]proxmox.ClusterResource, 0, len(pool.Members))

	for _, member := range pool.Members {
		if ig.isProxmoxResourceAnInstance(member) {
			out = append(out, member)
		}
	}

	return out, nil
}

// instanceState maps an instance's current name to the lifecycle state it
// represents, based on which InstanceName{Creating,Running,Removing} prefix
// it carries. ok is false if name doesn't match any known prefix.
func (ig *InstanceGroup) instanceState(name string) (provider.State, bool) {
	switch {
	case hasInstanceNamePrefix(name, ig.Settings.InstanceNameCreating):
		return provider.StateCreating, true
	case hasInstanceNamePrefix(name, ig.Settings.InstanceNameRunning):
		return provider.StateRunning, true
	case hasInstanceNamePrefix(name, ig.Settings.InstanceNameRemoving):
		return provider.StateDeleting, true
	default:
		return "", false
	}
}
