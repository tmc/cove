package main

import (
	"github.com/tmc/vz-macos/internal/controlclient"
)

// formatControlSocketDialError rewrites low-level dial errors with VM-aware
// guidance so callers can distinguish stopped vs booting/stale states.
func formatControlSocketDialError(sock string, err error) error {
	return controlclient.FormatDialError(sock, err)
}

func vmRunHintForSocket(sock string) string {
	return controlclient.RunHintForSocket(sock)
}
