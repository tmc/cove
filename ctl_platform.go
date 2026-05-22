package main

import (
	"path/filepath"

	agentstate "github.com/tmc/cove/internal/agent"
)

func ctlGuestPlatform(socketPath string) string {
	if socketPath == "" {
		return agentstate.Platform(vmDir)
	}
	return agentstate.Platform(filepath.Dir(socketPath))
}

func ctlGuestIsLinux(socketPath string) bool {
	return ctlGuestPlatform(socketPath) == agentstate.PlatformLinux
}
