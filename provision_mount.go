package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	di2 "github.com/tmc/apple/private/diskimages2"
	"github.com/tmc/apple/x/vzkit/disk"
)

// attachAndMountDataVolume attaches the disk image and mounts the Data volume.
// Returns the mount point path, the device identifier (for cleanup), and any error.
func attachAndMountDataVolume(diskPath string) (mountPoint, device, dataPartition string, err error) {
	provisionLog("attachAndMountDataVolume: diskPath=%s", diskPath)

	var lines []string
	var cmd *exec.Cmd
	var output []byte

	// Step 1: Attach the disk image without mounting
	device, err = attachDiskImageNoMountDI2(diskPath)
	if err != nil {
		provisionLog("diskimages2 attach failed, falling back to hdiutil: %v", err)
		cmd = exec.Command("hdiutil", "attach", diskPath, "-nobrowse", "-nomount")
		provisionLog("Running: hdiutil attach %s -nobrowse -nomount", diskPath)
		output, err = cmd.Output()
		if err != nil {
			provisionLog("hdiutil attach failed: %v", err)
			return "", "", "", fmt.Errorf("hdiutil attach failed: %w", err)
		}
		provisionLog("hdiutil output:\n%s", string(output))

		// Parse output to find the base disk device (e.g., /dev/disk19)
		// The output includes the base disk AND all synthesized APFS containers.
		baseDiskRe := regexp.MustCompile(`^(/dev/disk\d+)\s`)
		partitionRe := regexp.MustCompile(`^(/dev/disk\d+s\d+)\s`)
		lines = strings.Split(string(output), "\n")
		for _, line := range lines {
			if matches := baseDiskRe.FindStringSubmatch(line); matches != nil {
				if device == "" {
					device = matches[1]
					break
				}
			}
		}

		// Fallback: if no base disk found, extract from first partition.
		if device == "" {
			for _, line := range lines {
				if matches := partitionRe.FindStringSubmatch(line); matches != nil {
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
	}

	if device == "" {
		return "", "", "", fmt.Errorf("could not attach disk image: empty device")
	}

	provisionLog("Attached disk image to %s", device)

	// Step 2: Find the Data volume using diskutil list (without specifying device)
	// This shows ALL volumes including those in synthesized APFS containers
	cmd = exec.Command("diskutil", "list")
	output, err = cmd.Output()
	if err != nil {
		detachDisk(device)
		return "", "", "", fmt.Errorf("diskutil list failed: %w", err)
	}
	diskutilListOutput := string(output)

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
				provisionLog("Found APFS container: /dev/%s", containerDisk)
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
		return "", "", "", dataPartitionNotFoundError(device, diskutilListOutput)
	}

	provisionLog("Found Data partition: %s", dataPartition)

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

	provisionLog("Data volume mounted at: %s", mountPoint)
	return mountPoint, device, dataPartition, nil
}

func dataPartitionNotFoundError(device, diskutilListOutput string) error {
	return fmt.Errorf("could not find Data partition for disk %s\n\ndiskutil list output:\n%s", device, diskutilListOutput)
}

// attachDiskImageNoMountDI2 attaches a disk image using DiskImages2.framework
// and returns the base BSD device path (e.g. /dev/disk19). It disables
// automount to preserve existing provisioning behavior.
func attachDiskImageNoMountDI2(diskPath string) (string, error) {
	// The generated package loads the framework in init; class lookup verifies availability.
	if objc.GetClass("DiskImages2") == 0 {
		return "", fmt.Errorf("diskimages2 framework unavailable")
	}

	url := foundation.NewURLFileURLWithPath(diskPath)
	params, err := di2.NewDIAttachParamsWithURLError(url)
	if err != nil {
		return "", fmt.Errorf("diskimages2 init attach params: %w", err)
	}
	if params.ID == 0 {
		return "", fmt.Errorf("diskimages2 initWithURL returned nil")
	}
	params.SetAutoMount(false)

	handleIface, err := params.NewAttachWithError()
	if err != nil {
		return "", fmt.Errorf("diskimages2 attach: %w", err)
	}
	handle, ok := handleIface.(di2.DIDeviceHandle)
	if !ok || handle.ID == 0 {
		return "", fmt.Errorf("diskimages2 attach: unexpected handle type %T", handleIface)
	}
	defer handle.Release()

	if _, err := handle.WaitForDeviceWithError(); err != nil {
		return "", fmt.Errorf("diskimages2 wait for device: %w", err)
	}
	bsd := strings.TrimSpace(handle.BSDName())
	if bsd == "" {
		return "", fmt.Errorf("diskimages2 returned empty BSDName")
	}
	if strings.HasPrefix(bsd, "/dev/") {
		return bsd, nil
	}
	return "/dev/" + bsd, nil
}

// detachDisk safely detaches a disk device and verifies it is no longer
// attached. Falls back to escalating detach strategies if needed.
func detachDisk(device string) {
	detachDiskForPath(device, filepath.Join(vmDir, "disk.img"))
}

func detachDiskForPath(device, diskPath string) {
	fmt.Printf("Detaching %s...\n", device)

	if err := disk.EnsureDetached(diskPath); err != nil {
		fmt.Printf("warning: %v\n", err)
		fmt.Printf("  Manual fix: hdiutil detach %s -force\n", device)
	}
}

// checkVMNotRunning checks whether the VM is currently running by probing
// the control socket. Returns a clear error if the VM is active, preventing
// disk operations that would corrupt a running VM.
func checkVMNotRunning() error {
	return checkVMNotRunningAt(vmDir)
}

func checkVMNotRunningAt(vmDirectory string) error {
	sock := GetControlSocketPathForVM(vmDirectory)
	if _, err := os.Stat(sock); os.IsNotExist(err) {
		return nil // no socket, VM not running
	}
	conn, err := net.DialTimeout("unix", sock, 2*time.Second)
	if err != nil {
		// Socket exists but can't connect — stale socket, VM not running.
		return nil
	}
	conn.Close()
	return fmt.Errorf("vm is currently running (control socket active: %s)\n  Stop the VM first, then retry.\n  To stop: ./cove ctl shutdown", sock)
}

// checkDiskNotMounted checks if the disk is already mounted via hdiutil.
// If mounted and stdin is a terminal, offers to detach interactively.
func checkDiskNotMounted(diskPath string) error {
	device, found, err := disk.FindAttachedDisk(diskPath)
	if err != nil {
		// Log but don't block — if hdiutil info fails we proceed and let
		// hdiutil attach fail with a clearer error.
		provisionLog("warning: could not check disk attachment: %v", err)
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
		if _, stillFound, _ := disk.FindAttachedDisk(diskPath); stillFound {
			fmt.Println("Normal detach failed, trying force...")
			cmd := exec.Command("hdiutil", "detach", device, "-force")
			cmd.Run()
		}
		return nil
	}

	return fmt.Errorf("disk image is already mounted%s\n  Detach with: hdiutil detach %s -force\n  Or run: ./cove disk-detach", hint, device)
}

// pendingInstall represents a file that needs to be copied to a root-owned
// location with specific permissions. Used when the current process is not root.
type pendingInstall struct {
	Src  string      // temp file on host
	Dest string      // target path on mounted volume
	Mode os.FileMode // e.g. 0755
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
// elevated script that creates directories, copies pending files, and sets
// root:wheel ownership.
// APFS volumes from disk images have ownership disabled by default — without
// enableOwnership, chown silently does nothing even with sudo.
func fixOwnershipWithSudo(paths []string, dataPartition string, installs ...pendingInstall) error {
	return fixOwnershipWithSudoForVM(currentVMSelection(), paths, dataPartition, installs...)
}

func fixOwnershipWithSudoForVM(target vmSelection, paths []string, dataPartition string, installs ...pendingInstall) error {
	if len(paths) == 0 && len(installs) == 0 {
		return nil
	}

	total := len(paths) + len(installs)
	fmt.Printf("\n%d file(s) need root privileges.\n", total)

	// enableOwnership only persists the setting; the live mount keeps its
	// noowners flag until remounted. Without the in-place remount, every
	// chown silently no-ops and launchd later refuses the daemon because
	// the plist isn't owned by root:wheel. The typed manifest does the
	// remount, then copies, then chowns existing files in one elevated pass.
	em := &elevatedManifest{
		RemountOwners: []string{dataPartition},
	}
	for _, inst := range installs {
		em.MkdirAll = append(em.MkdirAll, filepath.Dir(inst.Dest))
		em.CopyFiles = append(em.CopyFiles, elevatedCopy{
			Src:   inst.Src,
			Dst:   inst.Dest,
			Mode:  fmt.Sprintf("%o", inst.Mode),
			Owner: "root:wheel",
		})
	}
	for _, p := range paths {
		em.ChownFiles = append(em.ChownFiles, elevatedChown{Path: p, Owner: "root:wheel"})
	}

	return runElevated(em, elevationPrompt(
		fmt.Sprintf("Fix file ownership on VM %q.", target.elevationLabel()),
	))
}
