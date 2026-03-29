// diskguard.go - Disk image attach/detach safety.
//
// Delegates to github.com/tmc/apple/x/vzkit/disk for the implementation.
package main

import (
	"fmt"
	"time"

	"github.com/tmc/apple/x/vzkit/disk"
)

// findAttachedDisk checks whether diskPath is currently attached via hdiutil.
func findAttachedDisk(diskPath string) (device string, found bool, err error) {
	return disk.FindAttachedDisk(diskPath)
}

// ensureDiskDetached detaches diskPath if it is currently attached.
func ensureDiskDetached(diskPath string) error {
	if err := disk.EnsureDetached(diskPath); err != nil {
		return err
	}
	fmt.Println("Disk detached successfully.")
	return nil
}

// detachDiskVerified detaches a device and verifies the disk is no longer attached.
// Note: this delegates to disk.EnsureDetached since the internal function is unexported.
func detachDiskVerified(device, diskPath string) error {
	return disk.EnsureDetached(diskPath)
}

// diskStillAttached reports whether diskPath is currently attached.
func diskStillAttached(diskPath string) bool {
	return disk.StillAttached(diskPath)
}

// waitForDiskAvailable polls until diskPath is no longer attached.
func waitForDiskAvailable(diskPath string, timeout time.Duration) error {
	return disk.WaitForAvailable(diskPath, timeout)
}
