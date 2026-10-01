package main

import (
	"context"
	"fmt"
	"time"

	vz "github.com/tmc/apple/virtualization"
)

func gracefulWindowShutdown(ctx context.Context, request func() error, state func() (vz.VZVirtualMachineState, error)) error {
	current, err := state()
	if err != nil {
		return fmt.Errorf("read VM state: %w", err)
	}
	if current == vz.VZVirtualMachineStateStopped {
		return nil
	}
	if current == vz.VZVirtualMachineStatePaused {
		return fmt.Errorf("resume the virtual machine before shutting it down")
	}
	if err := request(); err != nil {
		return fmt.Errorf("request shutdown: %w", err)
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		current, err := state()
		if err != nil {
			return fmt.Errorf("read VM state: %w", err)
		}
		if current == vz.VZVirtualMachineStateStopped {
			return nil
		}
		if current == vz.VZVirtualMachineStateError {
			return fmt.Errorf("virtual machine entered an error state during shutdown")
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for shutdown: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}
