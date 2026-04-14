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

// EnsureRecoveryDisk creates the recovery disk if it does not exist.
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

// CreateRecoveryDisk creates a FAT32 disk image with csrutil helper scripts.
func CreateRecoveryDisk(path string) error {
	fmt.Println("Creating recovery disk...")

	_ = os.Remove(path)
	dmgPath := path + ".dmg"
	_ = os.Remove(dmgPath)

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

	attachOut, err := exec.Command("hdiutil", "attach", dmgPath, "-nobrowse").CombinedOutput()
	if err != nil {
		_ = os.Remove(dmgPath)
		return fmt.Errorf("attach recovery disk: %w: %s", err, attachOut)
	}

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
		_ = os.Remove(dmgPath)
		return fmt.Errorf("could not find device in hdiutil output: %s", string(attachOut))
	}
	if mountPoint == "" {
		_ = exec.Command("hdiutil", "detach", device).Run()
		_ = os.Remove(dmgPath)
		return fmt.Errorf("could not find mount point in hdiutil output: %s", string(attachOut))
	}

	files := map[string]string{
		"csrutil-disable.sh": csrutilDisableScript,
		"csrutil-enable.sh":  csrutilEnableScript,
		"csrutil-status.sh":  csrutilStatusScript,
		"README.txt":         recoveryDiskReadme,
	}
	for name, content := range files {
		filePath := filepath.Join(mountPoint, name)
		if err := os.WriteFile(filePath, []byte(content), 0755); err != nil {
			_ = exec.Command("hdiutil", "detach", device).Run()
			_ = os.Remove(dmgPath)
			return fmt.Errorf("write %s: %w", name, err)
		}
	}

	if out, err := exec.Command("hdiutil", "detach", device).CombinedOutput(); err != nil {
		_ = exec.Command("hdiutil", "detach", device, "-force").Run()
		fmt.Printf("warning: detach %s: %s\n", device, out)
	}

	if err := os.Rename(dmgPath, path); err != nil {
		return fmt.Errorf("rename recovery disk: %w", err)
	}

	fmt.Printf("Recovery disk created: %s\n", path)
	return nil
}

const csrutilDisableScript = `#!/bin/bash
set -euo pipefail
echo "=== Disabling SIP ==="
csrutil disable
echo
csrutil status
echo
echo "Reboot to apply: reboot"
`

const csrutilEnableScript = `#!/bin/bash
set -euo pipefail
echo "=== Enabling SIP ==="
csrutil enable
echo
csrutil status
echo
echo "Reboot to apply: reboot"
`

const csrutilStatusScript = `#!/bin/bash
set -euo pipefail
echo "=== SIP Status ==="
csrutil status
`

const recoveryDiskReadme = `VZ-MACOS RECOVERY DISK
======================

This disk contains helper scripts for macOS Recovery operations.

USAGE
-----

1. Boot into Recovery Mode:
   cove run -recovery -gui -usb /path/to/recovery-disk.img

2. Open Terminal:
   Utilities > Terminal

3. Locate this disk:
   diskutil list
   cd /Volumes/VZRECOVERY

4. Run a script:
   sh csrutil-disable.sh
   sh csrutil-enable.sh
   sh csrutil-status.sh
`
