package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// RecoveryDiskPath returns the default recovery disk path for a VM.
func RecoveryDiskPath(vmDirectory string) string {
	return filepath.Join(vmDirectory, "recovery-disk.img")
}

// EnsureRecoveryDisk creates the recovery disk if it doesn't exist.
// Returns the path to the recovery disk image.
func EnsureRecoveryDisk(vmDirectory string) (string, error) {
	path := RecoveryDiskPath(vmDirectory)
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		return path, nil
	}
	if err := CreateRecoveryDisk(path); err != nil {
		return "", err
	}
	return path, nil
}

// CreateRecoveryDisk creates a FAT32 disk image with recovery scripts.
// The disk contains helper scripts for common recovery operations like
// disabling/enabling SIP via csrutil.
func CreateRecoveryDisk(path string) error {
	fmt.Println("Creating recovery disk...")

	// Remove stale files
	os.Remove(path)
	dmgPath := path + ".dmg"
	os.Remove(dmgPath)

	// Create a small FAT32 disk image (GPT + single FAT32 partition)
	out, err := exec.Command("hdiutil", "create",
		"-size", "64m",
		"-fs", "MS-DOS FAT32",
		"-volname", "VZRECOVERY",
		"-layout", "GPTSPUD",
		"-o", dmgPath,
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("create FAT32 disk image: %w: %s", err, out)
	}

	// Attach and mount the FAT32 partition
	attachOut, err := exec.Command("hdiutil", "attach", dmgPath, "-nobrowse").CombinedOutput()
	if err != nil {
		os.Remove(dmgPath)
		return fmt.Errorf("attach recovery disk: %w: %s", err, attachOut)
	}

	// Parse device and mount point from hdiutil attach output
	var device, mountPoint string
	for _, line := range strings.Split(strings.TrimSpace(string(attachOut)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		tabFields := strings.SplitN(line, "\t", 3)
		dev := strings.TrimSpace(tabFields[0])
		if !strings.HasPrefix(dev, "/dev/disk") {
			continue
		}
		if !strings.Contains(dev[len("/dev/disk"):], "s") {
			device = dev
		}
		if len(tabFields) >= 3 {
			mp := strings.TrimSpace(tabFields[2])
			if mp != "" && mp != "/" {
				mountPoint = mp
			}
		}
	}

	if device == "" {
		os.Remove(dmgPath)
		return fmt.Errorf("could not find device in hdiutil output: %s", string(attachOut))
	}
	if mountPoint == "" {
		if detachErr := exec.Command("hdiutil", "detach", device).Run(); detachErr != nil {
			fmt.Fprintf(os.Stderr, "warning: detach %s: %v\n", device, detachErr)
		}
		os.Remove(dmgPath)
		return fmt.Errorf("could not find mount point in hdiutil output: %s", string(attachOut))
	}

	// Write recovery scripts to the mounted volume
	scripts := map[string]string{
		"csrutil-disable.sh": csrutilDisableScript,
		"csrutil-enable.sh":  csrutilEnableScript,
		"csrutil-status.sh":  csrutilStatusScript,
		"README.txt":         recoveryDiskReadme,
	}

	for name, content := range scripts {
		filePath := filepath.Join(mountPoint, name)
		if err := os.WriteFile(filePath, []byte(content), 0755); err != nil {
			if detachErr := exec.Command("hdiutil", "detach", device).Run(); detachErr != nil {
				fmt.Fprintf(os.Stderr, "warning: detach %s: %v\n", device, detachErr)
			}
			os.Remove(dmgPath)
			return fmt.Errorf("write %s: %w", name, err)
		}
	}

	// Detach the disk
	if out, err := exec.Command("hdiutil", "detach", device).CombinedOutput(); err != nil {
		// Try force detach
		if forceErr := exec.Command("hdiutil", "detach", device, "-force").Run(); forceErr != nil {
			fmt.Fprintf(os.Stderr, "warning: force detach %s: %v\n", device, forceErr)
		}
		fmt.Printf("Warning: detach: %s\n", string(out))
	}

	// Rename .dmg to .img for consistency with other disk images
	if err := os.Rename(dmgPath, path); err != nil {
		return fmt.Errorf("rename recovery disk: %w", err)
	}

	fmt.Printf("Recovery disk created: %s\n", path)
	return nil
}

const csrutilDisableScript = `#!/bin/bash
# Disable System Integrity Protection (SIP)
#
# Run this script from Recovery Terminal:
#   diskutil list             # Find VZRECOVERY volume
#   cd /Volumes/VZRECOVERY
#   sh csrutil-disable.sh
#
# NOTE: csrutil requires authenticating as a user with SecureToken.
# If your user was created with 'inject -skip-setup-assistant', it
# may NOT have a SecureToken. In that case, you must first boot
# normally and let the user log in through Setup Assistant or
# loginwindow to receive a SecureToken.

echo "=== Disabling SIP ==="
echo
echo "This will disable System Integrity Protection."
echo "You will need to authenticate as an admin user with SecureToken."
echo
csrutil disable
echo
echo "SIP status after change:"
csrutil status
echo
echo "Reboot to apply: reboot"
`

const csrutilEnableScript = `#!/bin/bash
# Enable System Integrity Protection (SIP)
#
# Run this script from Recovery Terminal:
#   diskutil list             # Find VZRECOVERY volume
#   cd /Volumes/VZRECOVERY
#   sh csrutil-enable.sh

echo "=== Enabling SIP ==="
echo
csrutil enable
echo
echo "SIP status after change:"
csrutil status
echo
echo "Reboot to apply: reboot"
`

const csrutilStatusScript = `#!/bin/bash
# Check System Integrity Protection (SIP) status
#
# Run this script from Recovery Terminal:
#   diskutil list             # Find VZRECOVERY volume
#   cd /Volumes/VZRECOVERY
#   sh csrutil-status.sh

echo "=== SIP Status ==="
echo
csrutil status
`

const recoveryDiskReadme = `VZ-MACOS RECOVERY DISK
======================

This disk contains helper scripts for macOS Recovery operations.

USAGE
-----

1. Boot into Recovery Mode:
   vz-macos run -recovery -recovery-disk -gui

2. Open Terminal from the Recovery menu bar:
   Utilities > Terminal

3. Find this disk:
   diskutil list
   # Look for VZRECOVERY volume

4. Navigate to the disk:
   cd /Volumes/VZRECOVERY

5. Run a script:
   sh csrutil-disable.sh    # Disable SIP
   sh csrutil-enable.sh     # Enable SIP
   sh csrutil-status.sh     # Check SIP status

SCRIPTS
-------

csrutil-disable.sh  - Disable System Integrity Protection
csrutil-enable.sh   - Enable System Integrity Protection
csrutil-status.sh   - Check current SIP status

SECURETOKEN REQUIREMENT
-----------------------

csrutil requires authenticating as an admin user with a SecureToken.

Users created via 'vz-macos inject -skip-setup-assistant' do NOT receive
a SecureToken because Setup Assistant was bypassed. sysadminctl cannot
grant a SecureToken without an existing SecureToken holder.

To get a SecureToken:
  1. Boot the VM normally (not recovery)
  2. Let Setup Assistant run (don't use -skip-setup-assistant)
  3. Log in through Setup Assistant or loginwindow
  4. The user receives SecureToken on first interactive login

Alternatively, use 'vz-macos inject' without -skip-setup-assistant, then
boot with '-gui' to automate Setup Assistant via keyboard.
`
