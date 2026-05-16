package main

import (
	"fmt"
	"strings"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

func requireExistingVMForControl(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("vm name required")
	}
	dir, ok := vmconfig.ExistingPath(name)
	if !ok || !vmconfig.Validate(dir) {
		return "", fmt.Errorf("no VM named %q under %s\n  list VMs: cove list\n  create a VM: cove up -user <name>", name, vmconfig.BaseDir())
	}
	return dir, nil
}
