package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// attachAndMountDataVolume attaches the disk image and mounts the Data volume.
// Returns the mount point path, the device identifier (for cleanup), and any error.
func attachAndMountDataVolume(diskPath string) (mountPoint, device, dataPartition string, err error) {
	provisionLog("attachAndMountDataVolume: diskPath=%s", diskPath)

	// Step 1: Attach the disk image without mounting
	cmd := exec.Command("hdiutil", "attach", diskPath, "-nobrowse", "-nomount")
	provisionLog("Running: hdiutil attach %s -nobrowse -nomount", diskPath)
	output, err := cmd.Output()
	if err != nil {
		provisionLog("hdiutil attach failed: %v", err)
		return "", "", "", fmt.Errorf("hdiutil attach failed: %w", err)
	}
	provisionLog("hdiutil output:\n%s", string(output))

	// Parse output to find the base disk device (e.g., /dev/disk19)
	// The output includes the base disk AND all synthesized APFS containers
	// Output format:
	// /dev/disk19         	GUID_partition_scheme
	// /dev/disk19s1       	Apple_APFS_ISC
	// /dev/disk19s2       	Apple_APFS
	// /dev/disk20         	EF57347C-... (synthesized APFS container)
	// /dev/disk20s1       	41504653-... (APFS volume)
	// Parse output to find the base disk device
	// Look for /dev/diskN (without partition suffix like s1, s2)
	baseDiskRe := regexp.MustCompile(`^(/dev/disk\d+)\s`)
	partitionRe := regexp.MustCompile(`^(/dev/disk\d+s\d+)\s`)

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		// First, try to find a base disk (no partition suffix)
		if matches := baseDiskRe.FindStringSubmatch(line); matches != nil {
			if device == "" {
				device = matches[1]
				break
			}
		}
	}

	// Fallback: if no base disk found, extract from first partition
	if device == "" {
		for _, line := range lines {
			if matches := partitionRe.FindStringSubmatch(line); matches != nil {
				// Extract base disk from partition (e.g., /dev/disk19s1 -> /dev/disk19)
				partDiskRe := regexp.MustCompile(`^(/dev/disk\d+)s\d+$`)
				if baseMatches := partDiskRe.FindStringSubmatch(matches[1]); baseMatches != nil {
					device = baseMatches[1]
					break
				}
			}
		}
	}

	if device == "" {
		return "", "", "", fmt.Errorf("could not find device in hdiutil output: %s", string(output))
	}

	fmt.Printf("Attached disk image to %s\n", device)

	// Step 2: Find the Data volume using diskutil list (without specifying device)
	// This shows ALL volumes including those in synthesized APFS containers
	cmd = exec.Command("diskutil", "list")
	output, err = cmd.Output()
	if err != nil {
		detachDisk(device)
		return "", "", "", fmt.Errorf("diskutil list failed: %w", err)
	}

	// Look for APFS Volume named "Data" that belongs to our disk
	// We need to find volumes in containers that reference our physical disk
	// Format: 4:                APFS Volume Data                  320.9 MB   disk22s5
	allOutput := string(output)

	// First, find which APFS container is using our disk
	// Look for "Physical Store diskXsY" where X matches our device number
	deviceNum := strings.TrimPrefix(device, "/dev/disk")
	physStoreRe := regexp.MustCompile(`Physical Store disk` + regexp.QuoteMeta(deviceNum) + `s\d+`)

	// Find the container that uses our disk
	var containerDisk string
	containerRe := regexp.MustCompile(`/dev/(disk\d+) \(synthesized\)`)
	sections := strings.Split(allOutput, "/dev/disk")
	for i, section := range sections {
		if i == 0 {
			continue
		}
		section = "/dev/disk" + section
		if physStoreRe.MatchString(section) {
			// This section references our physical disk
			if matches := containerRe.FindStringSubmatch(section); matches != nil {
				containerDisk = matches[1]
				fmt.Printf("Found APFS container: /dev/%s\n", containerDisk)
				break
			}
		}
	}

	if containerDisk == "" {
		// Fallback: look for any "Data" volume in a recently attached container
		// The hdiutil output includes synthesized containers
		for _, line := range lines {
			fields := strings.Fields(line)
			if len(fields) > 0 && strings.HasPrefix(fields[0], "/dev/disk") && !strings.Contains(fields[0], "s") {
				// This is a container disk
				if fields[0] != device {
					containerDisk = strings.TrimPrefix(fields[0], "/dev/")
					break
				}
			}
		}
	}

	// Now find the Data volume in the container
	if containerDisk != "" {
		cmd = exec.Command("diskutil", "list", "/dev/"+containerDisk)
		output, err = cmd.Output()
		if err == nil {
			for _, line := range strings.Split(string(output), "\n") {
				lineLower := strings.ToLower(line)
				// Look for "APFS Volume Data" or similar
				if strings.Contains(lineLower, "apfs volume") && strings.Contains(lineLower, "data") && !strings.Contains(lineLower, "vm data") {
					fields := strings.Fields(line)
					for _, f := range fields {
						if strings.HasPrefix(f, containerDisk+"s") || strings.HasPrefix(f, "disk") && strings.Contains(f, "s") {
							dataPartition = "/dev/" + f
							break
						}
					}
					if dataPartition != "" {
						break
					}
				}
			}
		}
	}

	// Fallback: scan all synthesized containers from hdiutil output
	if dataPartition == "" {
		for _, line := range lines {
			fields := strings.Fields(line)
			if len(fields) >= 2 && strings.HasPrefix(fields[0], "/dev/disk") && strings.Contains(fields[0], "s") {
				// Check if this partition's container has a Data volume
				containerBase := strings.Split(strings.TrimPrefix(fields[0], "/dev/"), "s")[0]
				if containerBase != deviceNum { // Skip physical disk partitions
					cmd = exec.Command("diskutil", "list", "/dev/"+containerBase)
					checkOutput, err := cmd.Output()
					if err == nil {
						for _, checkLine := range strings.Split(string(checkOutput), "\n") {
							checkLower := strings.ToLower(checkLine)
							if strings.Contains(checkLower, "apfs volume") && strings.Contains(checkLower, "data") && !strings.Contains(checkLower, "vm data") {
								checkFields := strings.Fields(checkLine)
								for _, f := range checkFields {
									if strings.HasPrefix(f, "disk") && strings.Contains(f, "s") {
										dataPartition = "/dev/" + f
										break
									}
								}
								if dataPartition != "" {
									break
								}
							}
						}
					}
					if dataPartition != "" {
						break
					}
				}
			}
		}
	}

	if dataPartition == "" {
		detachDisk(device)
		return "", "", "", fmt.Errorf("could not find Data partition for disk %s", device)
	}

	fmt.Printf("Found Data partition: %s\n", dataPartition)

	// Step 3: Mount the Data partition
	cmd = exec.Command("diskutil", "mount", dataPartition)
	output, err = cmd.Output()
	if err != nil {
		detachDisk(device)
		return "", "", "", fmt.Errorf("diskutil mount failed: %w", err)
	}

	// Note: enableOwnership requires root and is handled later by
	// fixOwnershipWithSudo, which combines it with the chown step in a
	// single sudo call to minimize privilege escalation.

	// Step 4: Get the actual mount point from diskutil info
	// (don't guess - multiple "Data" volumes may be mounted)
	cmd = exec.Command("diskutil", "info", dataPartition)
	output, err = cmd.Output()
	if err != nil {
		detachDisk(device)
		return "", "", "", fmt.Errorf("diskutil info failed: %w", err)
	}

	// Parse mount point from diskutil info output
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Mount Point:") {
			mountPoint = strings.TrimSpace(strings.TrimPrefix(line, "Mount Point:"))
			break
		}
	}

	if mountPoint == "" {
		detachDisk(device)
		return "", "", "", fmt.Errorf("could not determine mount point for %s", dataPartition)
	}

	if _, err := os.Stat(mountPoint); os.IsNotExist(err) {
		detachDisk(device)
		return "", "", "", fmt.Errorf("mount point %s does not exist", mountPoint)
	}

	fmt.Printf("Data volume mounted at: %s\n", mountPoint)
	return mountPoint, device, dataPartition, nil
}

// detachDisk safely detaches a disk device and verifies it is no longer
// attached. Falls back to escalating detach strategies if needed.
func detachDisk(device string) {
	fmt.Printf("Detaching %s...\n", device)

	// Find the disk image path so we can verify detach.
	diskPath := filepath.Join(vmDir, "disk.img")

	if err := detachDiskVerified(device, diskPath); err != nil {
		fmt.Printf("Warning: %v\n", err)
		fmt.Printf("  Manual fix: hdiutil detach %s -force\n", device)
	}
}

// checkDiskNotMounted checks if the disk is already mounted via hdiutil.
// If mounted and stdin is a terminal, offers to detach interactively.
func checkDiskNotMounted(diskPath string) error {
	device, found, err := findAttachedDisk(diskPath)
	if err != nil {
		// Log but don't block — if hdiutil info fails we proceed and let
		// hdiutil attach fail with a clearer error.
		provisionLog("Warning: could not check disk attachment: %v", err)
		return nil
	}
	if !found {
		return nil
	}

	hint := ""
	if device != "" {
		hint = fmt.Sprintf(" (device: %s)", device)
	}

	// Offer to detach interactively.
	fmt.Printf("Disk image is already mounted%s.\n", hint)
	answer, err := readLine("Detach and continue? [Y/n] ")
	if err == nil {
		if strings.EqualFold(strings.TrimSpace(answer), "n") {
			return fmt.Errorf("disk image is already mounted%s", hint)
		}
		fmt.Printf("Detaching %s...\n", device)
		detachDisk(device)
		// Verify it's gone.
		if _, stillFound, _ := findAttachedDisk(diskPath); stillFound {
			fmt.Println("Normal detach failed, trying force...")
			cmd := exec.Command("hdiutil", "detach", device, "-force")
			cmd.Run()
		}
		return nil
	}

	return fmt.Errorf("disk image is already mounted%s\n  Detach with: hdiutil detach %s -force\n  Or run: ./vz-macos disk-detach", hint, device)
}

// chownRootWheel attempts to set ownership to root:wheel. If the process is not
// running as root, it records the path for a later targeted sudo chown call.
// This allows inject to run as a normal user, with only the chown step requiring sudo.
func chownRootWheel(path string, failedPaths *[]string) {
	if err := os.Chown(path, 0, 0); err != nil && failedPaths != nil {
		*failedPaths = append(*failedPaths, path)
	}
}

// fixOwnershipWithSudo enables APFS ownership on the volume and runs a single
// targeted sudo chown on files that need root:wheel ownership.
// APFS volumes from disk images have ownership disabled by default — without
// enableOwnership, chown silently does nothing even with sudo.
func fixOwnershipWithSudo(paths []string, dataPartition string) error {
	if len(paths) == 0 {
		return nil
	}
	fmt.Printf("\n%d file(s) need root:wheel ownership for launchd.\n", len(paths))

	// Build a shell script that enables ownership then chowns.
	script := fmt.Sprintf("diskutil enableOwnership %s && chown root:wheel", dataPartition)
	for _, p := range paths {
		script += fmt.Sprintf(" %q", p)
	}

	if os.Getuid() == 0 {
		fmt.Println("Running as root, setting ownership directly...")
		cmd := exec.Command("sh", "-c", script)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	// Use osascript to get authorization via a GUI prompt. This works
	// regardless of terminal/tty state (macgo replaces stdin with a pipe
	// and /dev/tty may not be available).
	fmt.Println("Requesting administrator privileges...")
	cmd := exec.Command("osascript", "-e",
		fmt.Sprintf(`do shell script %q with prompt "vz-macos needs to set file ownership on the VM disk for launchd." with administrator privileges`, script))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
