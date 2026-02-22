package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/tmc/appledocs/plist"
)

// hdiutilInfo represents the top-level structure returned by hdiutil info -plist.
type hdiutilInfo struct {
	Images []hdiutilImage `plist:"images"`
}

// hdiutilImage represents a single attached disk image entry.
type hdiutilImage struct {
	ImagePath   string             `plist:"image-path"`
	SystemImage bool               `plist:"system-image"`
	Entities    []hdiutilImagePart `plist:"system-entities"`
}

// hdiutilImagePart represents a partition within an attached image.
type hdiutilImagePart struct {
	DevEntry   string `plist:"dev-entry"`
	MountPoint string `plist:"mount-point"`
}

// findAttachedDisk checks whether diskPath is currently attached via hdiutil.
// If found, it returns the /dev/diskN device identifier. If the disk is not
// attached, found is false and err is nil. err is non-nil only when hdiutil
// info itself fails.
func findAttachedDisk(diskPath string) (device string, found bool, err error) {
	abs, err := filepath.Abs(diskPath)
	if err != nil {
		abs = diskPath
	}

	cmd := exec.Command("hdiutil", "info", "-plist")
	output, err := cmd.Output()
	if err != nil {
		return "", false, fmt.Errorf("hdiutil info: %w", err)
	}

	var info hdiutilInfo
	if _, err := plist.Unmarshal(output, &info); err != nil {
		return "", false, fmt.Errorf("parse hdiutil info plist: %w", err)
	}

	for _, img := range info.Images {
		if img.SystemImage {
			continue
		}
		imgAbs, _ := filepath.Abs(img.ImagePath)
		if imgAbs != abs && img.ImagePath != diskPath {
			continue
		}
		// Find the base device (no partition suffix) from entities.
		for _, ent := range img.Entities {
			if ent.DevEntry != "" {
				return ent.DevEntry, true, nil
			}
		}
		// Image matched but no entities — still counts as attached.
		return "", true, nil
	}

	return "", false, nil
}

// ensureDiskDetached detaches diskPath if it is currently attached.
// Returns nil immediately if the disk is not attached. On failure, returns
// an error with manual-fix instructions.
func ensureDiskDetached(diskPath string) error {
	device, found, err := findAttachedDisk(diskPath)
	if err != nil {
		return fmt.Errorf("check disk attachment: %w", err)
	}
	if !found {
		return nil
	}

	if device == "" {
		return fmt.Errorf("disk %s appears attached but no device found; run 'hdiutil info' to inspect", diskPath)
	}

	fmt.Printf("Disk %s is attached at %s — detaching...\n", diskPath, device)

	if err := detachDiskVerified(device, diskPath); err != nil {
		return fmt.Errorf("auto-detach failed: %w\n\nManual fix:\n  hdiutil detach %s -force\n  diskutil unmountDisk force %s", err, device, device)
	}

	fmt.Println("Disk detached successfully.")
	return nil
}

// detachDiskVerified detaches a device and verifies the disk image is no
// longer attached. It tries an escalating series of detach strategies.
func detachDiskVerified(device, diskPath string) error {
	// Attempt 1: graceful force detach.
	exec.Command("hdiutil", "detach", device, "-force").Run()
	if !diskStillAttached(diskPath) {
		return nil
	}

	// Attempt 2: unmount all volumes first, then detach.
	exec.Command("diskutil", "unmountDisk", "force", device).Run()
	exec.Command("hdiutil", "detach", device, "-force").Run()
	if !diskStillAttached(diskPath) {
		return nil
	}

	// Attempt 3: wait briefly and retry (VZ framework may hold a handle).
	time.Sleep(2 * time.Second)
	exec.Command("hdiutil", "detach", device, "-force").Run()
	if !diskStillAttached(diskPath) {
		return nil
	}

	return fmt.Errorf("disk %s still attached after multiple detach attempts", diskPath)
}

// diskStillAttached is a convenience wrapper around findAttachedDisk that
// returns true if the disk is attached and false otherwise (including on error).
func diskStillAttached(diskPath string) bool {
	_, found, _ := findAttachedDisk(diskPath)
	return found
}

// waitForDiskAvailable polls until diskPath is no longer attached, or
// timeout expires. This is useful after stopping a VM, since the VZ
// framework may hold the file handle briefly after StopWithCompletionHandler
// returns.
func waitForDiskAvailable(diskPath string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if !diskStillAttached(diskPath) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("disk %s still attached after %v", diskPath, timeout)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
