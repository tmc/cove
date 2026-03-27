package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// formatControlSocketDialError rewrites low-level dial errors with VM-aware
// guidance so callers can distinguish stopped vs booting/stale states.
func formatControlSocketDialError(sock string, err error) error {
	if err == nil {
		return nil
	}

	msg := strings.ToLower(err.Error())
	runHint := vmRunHintForSocket(sock)

	if os.IsNotExist(err) || strings.Contains(msg, "no such file or directory") {
		return fmt.Errorf("vm is not running: control socket not found at %s\n  start it with: %s", sock, runHint)
	}

	if strings.Contains(msg, "connection refused") {
		vmName := filepath.Base(filepath.Dir(sock))
		if _, statErr := os.Stat(sock); statErr == nil {
			return fmt.Errorf("vm %q control socket exists but is not accepting connections at %s\n  vm may still be booting or may have exited uncleanly\n  if booting: retry in a few seconds\n  if exited: restart with: %s", vmName, sock, runHint)
		}
		return fmt.Errorf("vm %q is not running: control socket unavailable at %s\n  start it with: %s", vmName, sock, runHint)
	}

	if strings.Contains(msg, "i/o timeout") ||
		strings.Contains(msg, "operation timed out") ||
		strings.Contains(msg, "resource temporarily unavailable") {
		return fmt.Errorf("vm control socket is present but not ready at %s\n  vm may still be booting; retry shortly", sock)
	}

	return fmt.Errorf("connect to control socket %s: %w", sock, err)
}

func vmRunHintForSocket(sock string) string {
	vmName := filepath.Base(filepath.Dir(sock))
	if vmName == "" || vmName == "." || vmName == string(filepath.Separator) {
		return "vz-macos run"
	}
	return fmt.Sprintf("vz-macos -vm %s run", vmName)
}
