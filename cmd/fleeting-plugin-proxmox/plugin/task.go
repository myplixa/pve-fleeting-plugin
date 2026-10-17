package plugin

import (
	"context"
	"fmt"
	"time"

	"github.com/luthermonson/go-proxmox"
)

// waitForTask waits for a Proxmox task to complete. err is the error from
// the call that produced task (e.g. vm.Config, vm.Start, vm.Delete); if that
// call already failed, waitForTask passes the error through unchanged
// without attempting to wait on a nil task. This lets call sites write
// `task, err := vm.SomeCall(ctx, ...); err = waitForTask(ctx, task, err,
// someTimeout)` instead of repeating the `if err == nil { err = task.Wait(...) }`
// guard at every call site.
func waitForTask(ctx context.Context, task *proxmox.Task, err error, timeout time.Duration) error {
	if err == nil {
		err = task.Wait(ctx, proxmoxTaskWaitInterval, timeout)
	}

	return err
}

// applyVMConfig runs vm.Config with the given options, waits for the
// resulting task, and wraps any failure with a message naming what was
// being configured. A no-op if options is empty, so callers can build up an
// options slice conditionally and call this unconditionally at the end.
func (ig *InstanceGroup) applyVMConfig(ctx context.Context, vm *proxmox.VirtualMachine, what string, options ...proxmox.VirtualMachineOption) error {
	if len(options) == 0 {
		return nil
	}

	task, err := vm.Config(ctx, options...)
	if err := waitForTask(ctx, task, err, proxmoxTaskWaitTimeout); err != nil {
		return fmt.Errorf("failed to configure instance %s: %w", what, err)
	}

	return nil
}
