package main

import (
	"fmt"
	"os"

	"github.com/tmc/cove/internal/mutationguard"
)

func acquireInstallerRunLock(dir string) (*RunLock, error) {
	if dir == "" {
		return nil, fmt.Errorf("install vm directory required")
	}
	guard, err := mutationguard.Acquire(coveRoot())
	if err != nil {
		return nil, fmt.Errorf("guard vm installation: %w", err)
	}
	defer guard.Release()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create install vm directory: %w", err)
	}
	lock, err := AcquireRunLock(dir)
	if err != nil {
		return nil, fmt.Errorf("lock vm installation: %w", err)
	}
	if err := guard.Release(); err != nil {
		lock.Release()
		return nil, fmt.Errorf("release installation guard: %w", err)
	}
	return lock, nil
}

func requireVMDirectory(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("stat vm directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("vm path is not a directory")
	}
	return nil
}
