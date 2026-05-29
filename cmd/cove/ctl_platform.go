package main

import (
	"os"
	"path/filepath"
	"strings"

	agentstate "github.com/tmc/cove/internal/agent"
)

func ctlGuestPlatform(socketPath string) string {
	if socketPath == "" {
		return agentstate.Platform(vmDir)
	}
	return agentstate.Platform(controlSocketVMDir(socketPath))
}

func controlSocketVMDir(socketPath string) string {
	if filepath.Base(socketPath) == "control.sock" {
		return filepath.Dir(socketPath)
	}
	if data, err := os.ReadFile(filepath.Join(filepath.Dir(socketPath), controlVMDirFileName)); err == nil {
		if dir := strings.TrimSpace(string(data)); dir != "" {
			return dir
		}
	}
	return filepath.Dir(socketPath)
}

func ctlGuestIsLinux(socketPath string) bool {
	return ctlGuestPlatform(socketPath) == agentstate.PlatformLinux
}
