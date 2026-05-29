package main

import (
	"errors"
	"fmt"
)

var errAppleAppSandboxHostAccessDenied = errors.New("apple app sandbox denies ambient host access")
var errPowerboxGrantRequired = errors.New("apple app sandbox requires a Powerbox or bookmark grant")

type powerboxGrantRequiredError struct {
	Action    string
	Key       string
	Kind      string
	StorePath string
}

func (e *powerboxGrantRequiredError) Error() string {
	return fmt.Sprintf("%s: %v for %s in %s", e.Action, errPowerboxGrantRequired, e.Key, e.StorePath)
}

func (e *powerboxGrantRequiredError) Unwrap() error {
	return errPowerboxGrantRequired
}

func appleAppSandboxActive() bool {
	return currentAppleAppSandboxStatus().Active
}

func denyAppleAppSandboxHostAccess(action string) error {
	if !appleAppSandboxActive() {
		return nil
	}
	return fmt.Errorf("%s: %w; use COVE_STATE_DIR for VM state or run outside the app sandbox until Powerbox grants are implemented", action, errAppleAppSandboxHostAccessDenied)
}

func powerboxGrantRequired(action, key, storePath string) error {
	return powerboxGrantRequiredKind(action, key, "vm-root", storePath)
}

func powerboxGrantRequiredKind(action, key, kind, storePath string) error {
	return &powerboxGrantRequiredError{
		Action:    action,
		Key:       key,
		Kind:      kind,
		StorePath: storePath,
	}
}
