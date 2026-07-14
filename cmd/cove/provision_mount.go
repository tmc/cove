package main

import (
	"errors"
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

	// Reconcile the attach-returned device against the authoritative,
	// image-path-keyed view from `hdiutil info -plist`. Under concurrent disk
	// images the attach path could report a stale/wrong /dev/diskN; the
	// image-path lookup returns the device that actually backs THIS image, so
	// downstream discovery and detach can never target an unrelated disk.
	if resolved, found, findErr := disk.FindAttachedDisk(diskPath); findErr == nil && found && resolved != "" {
		base := baseDiskDevice(resolved)
		if base != "" && base != device {
			provisionLog("reconciled attach device %s -> %s for image %s", device, base, diskPath)
			device = base
		}
	} else if findErr != nil {
		provisionLog("warning: could not confirm attached device for %s: %v", diskPath, findErr)
	}

	provisionLog("Attached disk image %s to %s", diskPath, device)

	// Step 2: Find our image's APFS Data volume. We anchor strictly on the
	// physical-store back-reference (the synthesized container whose Physical
	// Store points at OUR device) and deliberately do NOT fall back to picking
	// an arbitrary "recently attached" container — that generic fallback is
	// what let a concurrent image's disk be selected and detached (see the
	// VM-lifecycle report, finding 3).
	cmd = exec.Command("diskutil", "list")
	output, err = cmd.Output()
	if err != nil {
		detachDisk(device)
		return "", "", "", fmt.Errorf("diskutil list failed: %w", err)
	}
	diskutilListOutput := string(output)

	// Find which APFS container's Physical Store is one of our device's
	// partitions. Pure parse, so it is unit-tested directly.
	containerDisk := findAPFSContainerForDevice(diskutilListOutput, device)
	if containerDisk != "" {
		provisionLog("Found APFS container /dev/%s (Physical Store on %s)", containerDisk, device)
	}

	// Now find the Data volume in the container. Only accept a partition that
	// belongs to the confirmed container (containerDisk + "s"), so a same-named
	// "Data" volume in an unrelated container can never be selected.
	if containerDisk != "" {
		cmd = exec.Command("diskutil", "list", "/dev/"+containerDisk)
		output, err = cmd.Output()
		if err == nil {
			dataPartition = findDataVolumeInContainer(string(output), containerDisk)
		}
	}

	if dataPartition == "" {
		// No safe fallback: if we could not anchor the Data volume to our own
		// device's container, detach only our image and fail. Guessing a
		// container/volume here is exactly the wrong-disk hazard we are fixing.
		detachDisk(device)
		if containerDisk == "" {
			return "", "", "", fmt.Errorf("%w: no APFS container found whose Physical Store is on %s\n\ndiskutil list output:\n%s",
				ErrDataPartitionNotFound, device, diskutilListOutput)
		}
		return "", "", "", dataPartitionNotFoundError(device, diskutilListOutput)
	}

	// Safety check: the selected Data partition must belong to the confirmed
	// container. This is redundant with the search above but guards against a
	// future refactor reintroducing a cross-container selection.
	if containerDisk != "" && !strings.HasPrefix(dataPartition, "/dev/"+containerDisk+"s") {
		detachDisk(device)
		return "", "", "", fmt.Errorf("selected Data volume %s is not in our container /dev/%s (device %s)",
			dataPartition, containerDisk, device)
	}

	provisionLog("Selected Data volume %s in container /dev/%s (image %s, device %s)",
		dataPartition, containerDisk, diskPath, device)

	// Step 3: Mount the Data partition
	cmd = exec.Command("diskutil", "mount", dataPartition)
	if _, err = cmd.Output(); err != nil {
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

// ErrDataPartitionNotFound is returned when diskutil list output for a
// freshly attached disk image lacks an APFS Data volume entry. Callers
// can branch on this with errors.Is to surface a "image is not a
// macOS install disk" hint without parsing diskutil output.
var ErrDataPartitionNotFound = errors.New("data partition not found")

func dataPartitionNotFoundError(device, diskutilListOutput string) error {
	return fmt.Errorf("%w on disk %s\n\ndiskutil list output:\n%s", ErrDataPartitionNotFound, device, diskutilListOutput)
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

// findAPFSContainerForDevice returns the synthesized APFS container disk (e.g.
// "disk30") whose Physical Store is a partition of the given base device (e.g.
// "/dev/disk27"), by parsing `diskutil list` output. It returns "" if no
// container in the output references this exact device — it never guesses a
// container from an unrelated section, which is the wrong-disk hazard from the
// VM-lifecycle report (finding 3).
func findAPFSContainerForDevice(diskutilListOutput, device string) string {
	deviceNum := strings.TrimPrefix(device, "/dev/disk")
	if deviceNum == "" {
		return ""
	}
	// "Physical Store diskNsM" where diskN is exactly our base device (the
	// trailing sM and a word boundary prevent disk2 from matching disk27).
	physStoreRe := regexp.MustCompile(`Physical Store disk` + regexp.QuoteMeta(deviceNum) + `s\d+\b`)
	containerRe := regexp.MustCompile(`/dev/(disk\d+) \(synthesized\)`)

	sections := strings.Split(diskutilListOutput, "/dev/disk")
	for i, section := range sections {
		if i == 0 {
			continue
		}
		section = "/dev/disk" + section
		if physStoreRe.MatchString(section) {
			if matches := containerRe.FindStringSubmatch(section); matches != nil {
				return matches[1]
			}
		}
	}
	return ""
}

// findDataVolumeInContainer returns the /dev path of the APFS Data volume that
// belongs to containerDisk (e.g. "disk30"), by parsing the container's
// `diskutil list` output. Only a partition of THIS container (containerDisk +
// "s") is accepted, so a same-named "Data" volume in another container can
// never be selected. Returns "" if none is found.
func findDataVolumeInContainer(containerListOutput, containerDisk string) string {
	for _, line := range strings.Split(containerListOutput, "\n") {
		lineLower := strings.ToLower(line)
		if !strings.Contains(lineLower, "apfs volume") || !strings.Contains(lineLower, "data") || strings.Contains(lineLower, "vm data") {
			continue
		}
		for _, f := range strings.Fields(line) {
			if strings.HasPrefix(f, containerDisk+"s") {
				return "/dev/" + f
			}
		}
	}
	return ""
}

// baseDiskDevice returns the base /dev/diskN device for a device path that may
// be a partition (e.g. /dev/disk27s2 -> /dev/disk27). Paths that are already a
// base device, or that don't match the expected shape, are returned unchanged.
func baseDiskDevice(device string) string {
	m := baseDiskRe.FindStringSubmatch(device)
	if m == nil {
		return device
	}
	return m[1]
}

var baseDiskRe = regexp.MustCompile(`^(/dev/disk\d+)(?:s\d+)?$`)

// detachDisk safely detaches a disk device and verifies it is no longer
// attached. Falls back to escalating detach strategies if needed.
func detachDisk(device string) {
	detachDiskForPath(device, filepath.Join(vmDir, "disk.img"))
}

var (
	ensureDetachedHook       = disk.EnsureDetached
	forceDetachViaHelperHook = forceDetachViaHelper
)

func detachDiskForPath(device, diskPath string) {
	fmt.Printf("Detaching %s...\n", device)

	if err := ensureDetachedHook(diskPath); err != nil {
		fmt.Printf("warning: %v\n", err)
		if helperInstalled() {
			fmt.Println("  Trying cove-helper force detach...")
			if helperErr := forceDetachViaHelperHook(device, diskPath); helperErr == nil {
				fmt.Println("  Detached via cove-helper.")
				return
			} else {
				fmt.Printf("  cove-helper force detach failed: %v\n", helperErr)
				return
			}
		}
		fmt.Printf("  Manual fix: hdiutil detach %s -force\n", device)
	}
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
			if helperInstalled() {
				fmt.Println("Normal detach failed, trying cove-helper force detach...")
				if err := forceDetachViaHelperHook(device, diskPath); err != nil {
					return fmt.Errorf("disk image is already mounted%s; cove-helper force detach failed: %w", hint, err)
				}
			} else {
				fmt.Println("Normal detach failed, trying force...")
				cmd := exec.Command("hdiutil", "detach", device, "-force")
				cmd.Run()
			}
		}
		return nil
	}

	if helperInstalled() {
		return fmt.Errorf("disk image is already mounted%s; cove-helper is installed but cove could not prompt to detach interactively", hint)
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

// fixOwnershipWithSudoForVM enables APFS ownership on the volume and runs a single
// elevated script that creates directories, copies pending files, and sets
// root:wheel ownership.
// APFS volumes from disk images have ownership disabled by default — without
// enableOwnership, chown silently does nothing even with sudo.
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
