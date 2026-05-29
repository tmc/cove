package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestDetachDiskForPathUsesHelperWithoutManualCommand(t *testing.T) {
	oldEnsure := ensureDetachedHook
	oldHelperInstalled := helperInstalled
	oldForce := forceDetachViaHelperHook
	t.Cleanup(func() {
		ensureDetachedHook = oldEnsure
		helperInstalled = oldHelperInstalled
		forceDetachViaHelperHook = oldForce
	})
	ensureDetachedHook = func(string) error {
		return fmt.Errorf("auto-detach failed: disk busy")
	}
	helperInstalled = func() bool { return true }
	var gotDevice, gotDiskPath string
	forceDetachViaHelperHook = func(device, diskPath string) error {
		gotDevice, gotDiskPath = device, diskPath
		return nil
	}

	out := captureStdout(t, func() error {
		detachDiskForPath("/dev/disk23", "/Users/tmc/.vz/vms/test/disk.img")
		return nil
	})
	if gotDevice != "/dev/disk23" || gotDiskPath != "/Users/tmc/.vz/vms/test/disk.img" {
		t.Fatalf("forceDetachViaHelper(%q, %q)", gotDevice, gotDiskPath)
	}
	if strings.Contains(out, "Manual fix:") || strings.Contains(out, "hdiutil detach") {
		t.Fatalf("output contains manual command:\n%s", out)
	}
	if !strings.Contains(out, "Detached via cove-helper.") {
		t.Fatalf("output missing helper success:\n%s", out)
	}
}

func TestDetachDiskForPathPrintsManualCommandWithoutHelper(t *testing.T) {
	oldEnsure := ensureDetachedHook
	oldHelperInstalled := helperInstalled
	t.Cleanup(func() {
		ensureDetachedHook = oldEnsure
		helperInstalled = oldHelperInstalled
	})
	ensureDetachedHook = func(string) error {
		return fmt.Errorf("auto-detach failed: disk busy")
	}
	helperInstalled = func() bool { return false }

	out := captureStdout(t, func() error {
		detachDiskForPath("/dev/disk23", "/Users/tmc/.vz/vms/test/disk.img")
		return nil
	})
	if !strings.Contains(out, "Manual fix: hdiutil detach /dev/disk23 -force") {
		t.Fatalf("output missing manual fallback:\n%s", out)
	}
}
