package main

import (
	"errors"
	"fmt"
)

var errAppleAppSandboxHostAccessDenied = errors.New("apple app sandbox denies ambient host access")

func appleAppSandboxActive() bool {
	return currentAppleAppSandboxStatus().Active
}

func denyAppleAppSandboxHostAccess(action string) error {
	if !appleAppSandboxActive() {
		return nil
	}
	return fmt.Errorf("%s: %w; use COVE_STATE_DIR for VM state or run outside the app sandbox until Powerbox grants are implemented", action, errAppleAppSandboxHostAccessDenied)
}
