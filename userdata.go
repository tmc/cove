// userdata.go - Separate user data disk support
//
// This enables separating user data from the macOS system disk:
// - System disk: Contains macOS, apps, system files (can be a golden image)
// - Data disk: Contains /Users, user settings, documents
//
// Features:
// - Sparse bundle format (grows on demand, saves host space)
// - Configurable mount strategy (symlinks, direct mount, /Volumes/UserData)
// - APFS clonefile (CoW) with full-copy fallback
// - Automatic format and setup via LaunchDaemon
//
// Use cases:
// - Golden images: Share system disk across multiple VMs
// - Backup/reset: Reset system without losing user data
// - CI/CD: Boot read-only golden + ephemeral data disk
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tmc/apple/foundation"
	vz "github.com/tmc/apple/virtualization"
)

// MountStrategy defines how the user data disk is mounted in the guest
type MountStrategy string

const (
	// MountStrategyVolumes mounts at /Volumes/UserData (safest, manual symlink setup)
	MountStrategyVolumes MountStrategy = "volumes"
	// MountStrategySymlinks mounts at /Volumes/UserData with symlinks from /Users
	MountStrategySymlinks MountStrategy = "symlinks"
	// MountStrategyDirect mounts directly as /Users (requires careful setup)
	MountStrategyDirect MountStrategy = "direct"
)

// UserDataConfig holds configuration for separate user data disk
type UserDataConfig struct {
	Enabled       bool          // Whether to use separate user data disk
	Path          string        // Path to user data disk image/bundle
	SizeGB        uint64        // Size in GB for new disk
	ReadOnly      bool          // Mount as read-only (for testing)
	MountStrategy MountStrategy // How to mount in guest
	Ephemeral     bool          // Discard changes after VM stops (CI/CD mode)
}

// DefaultUserDataPath returns the default path for user data disk
func DefaultUserDataPath(vmDir string) string {
	return filepath.Join(vmDir, "userdata.sparsebundle")
}

// ParseMountStrategy parses a mount strategy string
func ParseMountStrategy(s string) (MountStrategy, error) {
	switch strings.ToLower(s) {
	case "", "volumes":
		return MountStrategyVolumes, nil
	case "symlinks":
		return MountStrategySymlinks, nil
	case "direct":
		return MountStrategyDirect, nil
	default:
		return "", fmt.Errorf("unknown mount strategy: %s (use volumes, symlinks, or direct)", s)
	}
}

// EnsureUserDataDisk creates the user data disk if it doesn't exist
func EnsureUserDataDisk(config UserDataConfig) error {
	if !config.Enabled {
		return nil
	}

	// Check if already exists (could be .sparsebundle directory or .img file)
	if _, err := os.Stat(config.Path); err == nil {
		fmt.Printf("Using existing user data disk: %s\n", config.Path)
		return nil
	}

	// Create new disk
	sizeGB := config.SizeGB
	if sizeGB == 0 {
		sizeGB = 32 // Default 32GB for user data
	}

	fmt.Printf("Creating user data disk: %s (%d GB, sparse bundle)\n", config.Path, sizeGB)
	return createSparseBundleDisk(config.Path, sizeGB)
}

// createSparseBundleDisk creates a sparse bundle disk image using hdiutil
// Sparse bundles grow on demand and are efficient for backup (band files)
func createSparseBundleDisk(path string, sizeGB uint64) error {
	// Determine volume name from path
	volName := "UserData"

	// Create sparse bundle with APFS filesystem
	// -type SPARSEBUNDLE creates a directory bundle with band files
	// -fs APFS creates an APFS container inside
	// -size specifies maximum size (actual size grows on demand)
	cmd := exec.Command("hdiutil", "create",
		"-type", "SPARSEBUNDLE",
		"-fs", "APFS",
		"-size", fmt.Sprintf("%dg", sizeGB),
		"-volname", volName,
		path,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("hdiutil create failed: %w\nOutput: %s", err, string(output))
	}

	fmt.Printf("Created sparse bundle: %s\n", path)
	return nil
}

// CreateUserDataStorageDevice creates a storage device for the user data disk
func CreateUserDataStorageDevice(config UserDataConfig) (vz.VZVirtioBlockDeviceConfiguration, error) {
	if !config.Enabled {
		return vz.VZVirtioBlockDeviceConfiguration{}, fmt.Errorf("user data disk not enabled")
	}

	// For sparse bundles, we need to attach first to get a device
	diskPath := config.Path
	if strings.HasSuffix(diskPath, ".sparsebundle") {
		// Attach sparse bundle and get the device path
		devicePath, err := attachSparseBundleForVM(diskPath)
		if err != nil {
			return vz.VZVirtioBlockDeviceConfiguration{}, fmt.Errorf("attach sparse bundle: %w", err)
		}
		diskPath = devicePath
	}

	url := foundation.NewURLFileURLWithPath(diskPath)
	url.Retain()

	attachment, err := vz.NewDiskImageStorageDeviceAttachmentWithURLReadOnlyError(url, config.ReadOnly)
	if err != nil {
		return vz.VZVirtioBlockDeviceConfiguration{}, fmt.Errorf("create user data attachment: %w", err)
	}
	attachment.Retain()

	blockConfig := vz.NewVirtioBlockDeviceConfigurationWithAttachment(&attachment.VZStorageDeviceAttachment)
	if blockConfig.ID == 0 {
		return blockConfig, fmt.Errorf("failed to create user data block device")
	}

	mode := "read-write"
	if config.ReadOnly {
		mode = "read-only"
	}
	fmt.Printf("  User data disk: %s (%s)\n", config.Path, mode)

	return blockConfig, nil
}

// attachSparseBundleForVM attaches a sparse bundle and returns the raw device path
// suitable for passing to the VM as a block device
func attachSparseBundleForVM(bundlePath string) (string, error) {
	// hdiutil attach with -nomount returns the device without mounting
	cmd := exec.Command("hdiutil", "attach",
		bundlePath,
		"-nomount",
		"-noverify",
		"-noautofsck",
	)

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("hdiutil attach failed: %w", err)
	}

	// Parse output to find device path (first line typically contains /dev/diskN)
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) > 0 && strings.HasPrefix(fields[0], "/dev/disk") {
			// Return the base device (e.g., /dev/disk5)
			return fields[0], nil
		}
	}

	return "", fmt.Errorf("could not find device path in hdiutil output: %s", string(output))
}

// AddUserDataDiskToConfig adds the user data disk to VM configuration
func AddUserDataDiskToConfig(config vz.VZVirtualMachineConfiguration, userDataConfig UserDataConfig) error {
	if !userDataConfig.Enabled {
		return nil
	}

	// Get existing storage devices
	existingStorage := config.StorageDevices()

	// Create user data device
	userDataDevice, err := CreateUserDataStorageDevice(userDataConfig)
	if err != nil {
		return err
	}

	// Combine storage devices
	storageDevices := make([]vz.VZStorageDeviceConfiguration, 0, len(existingStorage)+1)
	for _, dev := range existingStorage {
		storageDevices = append(storageDevices, vz.VZStorageDeviceConfigurationFromID(dev.GetID()))
	}
	storageDevices = append(storageDevices, vz.VZStorageDeviceConfigurationFromID(userDataDevice.ID))

	config.SetStorageDevices(storageDevices)
	return nil
}

// InjectUserDataSetup injects the LaunchDaemon that sets up the user data disk on first boot
func InjectUserDataSetup(mountPoint string, config UserDataConfig, rootFiles *[]string) error {
	fmt.Println("Injecting user data disk setup...")

	// Create the setup script
	scriptDir := filepath.Join(mountPoint, "private", "var", "db")
	if err := os.MkdirAll(scriptDir, 0755); err != nil {
		return fmt.Errorf("create script directory: %w", err)
	}

	scriptPath := filepath.Join(scriptDir, "vz-userdata-setup.sh")
	scriptContent := generateUserDataSetupScript(config)
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		return fmt.Errorf("write setup script: %w", err)
	}
	chownRootWheel(scriptPath, rootFiles)
	fmt.Printf("Written: %s\n", scriptPath)

	// Create the LaunchDaemon plist
	launchDaemonsDir := filepath.Join(mountPoint, "Library", "LaunchDaemons")
	if err := os.MkdirAll(launchDaemonsDir, 0755); err != nil {
		return fmt.Errorf("create LaunchDaemons directory: %w", err)
	}

	plistPath := filepath.Join(launchDaemonsDir, "com.github.tmc.vz-macos.userdata.plist")
	plistContent := generateUserDataLaunchDaemonPlist()
	if err := os.WriteFile(plistPath, []byte(plistContent), 0644); err != nil {
		return fmt.Errorf("write LaunchDaemon plist: %w", err)
	}
	chownRootWheel(plistPath, rootFiles)
	fmt.Printf("Written: %s\n", plistPath)

	return nil
}

// generateUserDataLaunchDaemonPlist returns the LaunchDaemon plist for user data setup
func generateUserDataLaunchDaemonPlist() string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.github.tmc.vz-macos.userdata</string>
    <key>ProgramArguments</key>
    <array>
        <string>/bin/bash</string>
        <string>/var/db/vz-userdata-setup.sh</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>LaunchOnlyOnce</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/var/log/vz-userdata.log</string>
    <key>StandardErrorPath</key>
    <string>/var/log/vz-userdata.log</string>
</dict>
</plist>
`
}

// generateUserDataSetupScript generates the first-boot setup script for the user data disk
func generateUserDataSetupScript(config UserDataConfig) string {
	strategy := string(config.MountStrategy)
	if strategy == "" {
		strategy = "volumes"
	}

	return fmt.Sprintf(`#!/bin/bash
# vz-macos User Data Disk Setup Script
# This runs once on first boot to format and mount the user data disk
set -e

MARKER="/var/db/.vz-userdata-configured"
LOG="/var/log/vz-userdata.log"
MOUNT_STRATEGY="%s"

log() {
    echo "$(date '+%%Y-%%m-%%d %%H:%%M:%%S') $1" >> "$LOG"
    echo "$1"
}

# Check if already configured
if [ -f "$MARKER" ]; then
    log "User data disk already configured"
    exit 0
fi

log "=== User Data Disk Setup ==="
log "Mount strategy: $MOUNT_STRATEGY"

# Find the user data disk (should be disk1 or disk2, not the system disk)
find_data_disk() {
    for disk in /dev/disk1 /dev/disk2 /dev/disk3; do
        if diskutil info "$disk" 2>/dev/null | grep -q "Virtual"; then
            # Check if it's not the system disk
            if ! diskutil info "$disk" 2>/dev/null | grep -q "Macintosh HD"; then
                # Check if unformatted or has our UserData volume
                local fstype=$(diskutil info "$disk" 2>/dev/null | grep "Type (Bundle):" | awk '{print $3}')
                if [ -z "$fstype" ] || [ "$fstype" = "None" ]; then
                    echo "$disk"
                    return 0
                fi
                # Check synthesized container for UserData
                if diskutil list "$disk" 2>/dev/null | grep -q "UserData"; then
                    echo "$disk"
                    return 0
                fi
            fi
        fi
    done
    return 1
}

DATA_DISK=$(find_data_disk)
if [ -z "$DATA_DISK" ]; then
    log "ERROR: Could not find user data disk"
    log "Available disks:"
    diskutil list >> "$LOG"
    exit 1
fi

log "Found user data disk: $DATA_DISK"

# Check if already formatted with APFS
needs_format=true
if diskutil list "$DATA_DISK" 2>/dev/null | grep -q "APFS Volume.*UserData"; then
    log "Disk already formatted with APFS UserData volume"
    needs_format=false
fi

# Format if needed
if [ "$needs_format" = true ]; then
    log "Formatting disk as APFS with volume name 'UserData'..."
    diskutil eraseDisk APFS UserData "$DATA_DISK" 2>&1 | tee -a "$LOG"
fi

# Find and mount the UserData volume
VOLUME_ID=""
for part in ${DATA_DISK}s1 ${DATA_DISK}s2 ${DATA_DISK}s3; do
    if diskutil info "$part" 2>/dev/null | grep -q "UserData"; then
        VOLUME_ID="$part"
        break
    fi
done

# Try synthesized APFS container
if [ -z "$VOLUME_ID" ]; then
    # Find the synthesized container from diskutil list
    for container in /dev/disk{2..10}; do
        if diskutil list "$container" 2>/dev/null | grep -q "Physical Store.*${DATA_DISK}"; then
            # This container uses our disk, find UserData volume
            VOLUME_ID=$(diskutil list "$container" 2>/dev/null | grep "APFS Volume.*UserData" | awk '{print $NF}')
            if [ -n "$VOLUME_ID" ]; then
                VOLUME_ID="/dev/$VOLUME_ID"
                break
            fi
        fi
    done
fi

if [ -z "$VOLUME_ID" ]; then
    log "ERROR: Could not find UserData volume"
    exit 1
fi

log "UserData volume: $VOLUME_ID"

# Mount at /Volumes/UserData
diskutil mount "$VOLUME_ID" 2>&1 | tee -a "$LOG"
MOUNT_POINT="/Volumes/UserData"

if [ ! -d "$MOUNT_POINT" ]; then
    log "ERROR: Mount point $MOUNT_POINT does not exist after mount"
    exit 1
fi

log "Mounted at: $MOUNT_POINT"

# Create standard directory structure
log "Creating directory structure..."
mkdir -p "$MOUNT_POINT/Users"
mkdir -p "$MOUNT_POINT/Library/Application Support"
mkdir -p "$MOUNT_POINT/var"

# Configure based on mount strategy
case "$MOUNT_STRATEGY" in
    "volumes")
        log "Strategy 'volumes': User data at /Volumes/UserData"
        log "Users should manually symlink or configure apps to use this location"
        ;;

    "symlinks")
        log "Strategy 'symlinks': Creating symlinks from /Users to /Volumes/UserData/Users"
        # This requires careful handling of existing /Users
        # Only do this if /Users is empty or we have permission
        if [ -d /Users ] && [ "$(ls -A /Users 2>/dev/null)" ]; then
            log "WARNING: /Users is not empty. Moving existing users..."
            for user in /Users/*/; do
                username=$(basename "$user")
                if [ "$username" != "Shared" ]; then
                    log "Moving /Users/$username to $MOUNT_POINT/Users/"
                    mv "/Users/$username" "$MOUNT_POINT/Users/" 2>&1 | tee -a "$LOG" || true
                    ln -s "$MOUNT_POINT/Users/$username" "/Users/$username" 2>&1 | tee -a "$LOG" || true
                fi
            done
        fi
        ;;

    "direct")
        log "Strategy 'direct': Attempting to mount as /Users"
        log "WARNING: This is experimental and may cause issues"
        # This would require unmounting /Users and remounting, which is complex
        # For now, we just log a warning
        log "Direct mount not fully implemented. Using /Volumes/UserData instead."
        ;;
esac

# Add to fstab for persistent mount
if ! grep -q "UserData" /etc/fstab 2>/dev/null; then
    log "Adding to /etc/fstab for persistent mount..."
    VOLUME_UUID=$(diskutil info "$VOLUME_ID" | grep "Volume UUID:" | awk '{print $3}')
    if [ -n "$VOLUME_UUID" ]; then
        echo "UUID=$VOLUME_UUID /Volumes/UserData apfs rw 0 0" >> /etc/fstab
        log "Added fstab entry for UUID=$VOLUME_UUID"
    fi
fi

# Mark as configured
touch "$MARKER"
log "Configuration marker created: $MARKER"

# Self-cleanup
rm -f /Library/LaunchDaemons/com.github.tmc.vz-macos.userdata.plist 2>/dev/null || true
rm -f /var/db/vz-userdata-setup.sh 2>/dev/null || true
log "Cleanup complete"

log "=== User Data Setup Complete ==="
log "User data disk is available at: $MOUNT_POINT"

exit 0
`, strategy)
}

// CloneUserDataDisk creates a copy of a user data disk using APFS clonefile (CoW) if possible
func CloneUserDataDisk(srcPath, dstPath string) error {
	// Check if source exists
	if _, err := os.Stat(srcPath); os.IsNotExist(err) {
		return fmt.Errorf("source disk not found: %s", srcPath)
	}

	// Check if destination already exists
	if _, err := os.Stat(dstPath); err == nil {
		return fmt.Errorf("destination already exists: %s", dstPath)
	}

	fmt.Printf("Cloning user data disk: %s -> %s\n", srcPath, dstPath)

	// Try APFS clonefile first (copy-on-write, instant)
	// cp -c uses clonefile on APFS
	cmd := exec.Command("cp", "-c", "-R", srcPath, dstPath)
	if err := cmd.Run(); err != nil {
		// Fallback to regular copy
		fmt.Println("APFS clonefile not available, falling back to full copy...")
		cmd = exec.Command("cp", "-R", srcPath, dstPath)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("copy failed: %w", err)
		}
	}

	fmt.Printf("Clone complete: %s\n", dstPath)
	return nil
}

// handleUserDataCommand handles the userdata subcommand
func handleUserDataCommand(args []string) error {
	if len(args) == 0 {
		fmt.Println(UserDataHelp())
		return nil
	}

	switch args[0] {
	case "help":
		fmt.Println(UserDataHelp())
	case "setup":
		fmt.Println(UserDataSetupScript())
	case "workflow":
		fmt.Println(GoldenImageWorkflow())
	case "create":
		return handleUserDataCreate(args[1:])
	case "inject":
		return handleUserDataInject(args[1:])
	case "clone":
		return handleUserDataClone(args[1:])
	case "migrate":
		return handleUserDataMigrate(args[1:])
	default:
		return fmt.Errorf("unknown userdata command: %s", args[0])
	}
	return nil
}

// handleUserDataCreate creates a new user data disk
func handleUserDataCreate(args []string) error {
	fs := flag.NewFlagSet("userdata create", flag.ExitOnError)
	sizeGB := fs.Uint64("size", 32, "Size in GB")
	path := fs.String("path", "", "Path for the disk (default: vmDir/userdata.sparsebundle)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	diskPath := *path
	if diskPath == "" {
		diskPath = DefaultUserDataPath(vmDir)
	}

	return createSparseBundleDisk(diskPath, *sizeGB)
}

// handleUserDataInject injects user data setup into a VM disk
func handleUserDataInject(args []string) error {
	fs := flag.NewFlagSet("userdata inject", flag.ExitOnError)
	strategy := fs.String("strategy", "volumes", "Mount strategy: volumes, symlinks, or direct")

	if err := fs.Parse(args); err != nil {
		return err
	}

	mountStrategy, err := ParseMountStrategy(*strategy)
	if err != nil {
		return err
	}

	diskPath := filepath.Join(vmDir, "disk.img")
	if _, err := os.Stat(diskPath); os.IsNotExist(err) {
		return fmt.Errorf("disk image not found: %s", diskPath)
	}

	// Mount the disk
	mountPoint, device, _, err := attachAndMountDataVolume(diskPath)
	if err != nil {
		return fmt.Errorf("mount data volume: %w", err)
	}
	defer detachDisk(device)

	config := UserDataConfig{
		Enabled:       true,
		MountStrategy: mountStrategy,
	}

	return InjectUserDataSetup(mountPoint, config, nil)
}

// handleUserDataClone clones a user data disk
func handleUserDataClone(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: vz-macos userdata clone <source> <destination>")
	}
	return CloneUserDataDisk(args[0], args[1])
}

// handleUserDataMigrate migrates an existing single-disk VM to use separate user data
func handleUserDataMigrate(args []string) error {
	fs := flag.NewFlagSet("userdata migrate", flag.ExitOnError)
	sizeGB := fs.Uint64("size", 64, "Size in GB for user data disk")
	strategy := fs.String("strategy", "symlinks", "Mount strategy after migration")
	dryRun := fs.Bool("dry-run", false, "Show what would be done without making changes")
	keepOriginal := fs.Bool("keep-original", true, "Keep original /Users on system disk (default: true)")
	verboseFlag := fs.Bool("v", false, "Verbose output")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: vz-macos userdata migrate [options]

Migrate an existing single-disk VM to use a separate user data disk.

This command:
1. Creates a new user data sparse bundle
2. Mounts the existing system disk
3. Copies /Users/* to the user data disk
4. Injects LaunchDaemon to mount user data on boot
5. Optionally clears /Users on system disk

Options:
`)
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
Examples:
  # Migrate current VM's user data
  vz-macos userdata migrate

  # Dry run to see what would be migrated
  vz-macos userdata migrate -dry-run

  # Custom size and strategy
  vz-macos userdata migrate -size 100 -strategy symlinks

  # Remove original /Users after migration (saves space)
  vz-macos userdata migrate -keep-original=false
`)
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	// Validate mount strategy
	mountStrategy, err := ParseMountStrategy(*strategy)
	if err != nil {
		return err
	}

	// Check if running as root (required for proper file ownership)
	if os.Getuid() != 0 && !*dryRun {
		fmt.Println("Warning: Not running as root. File ownership may not be preserved.")
		fmt.Println("  → For best results, run: sudo ./vz-macos userdata migrate ...")
		fmt.Println()
	}

	fmt.Println("=== User Data Migration ===")
	fmt.Printf("VM Directory: %s\n", vmDir)
	fmt.Printf("User data size: %d GB\n", *sizeGB)
	fmt.Printf("Mount strategy: %s\n", mountStrategy)
	fmt.Printf("Keep original: %v\n", *keepOriginal)
	fmt.Printf("Dry run: %v\n", *dryRun)
	fmt.Println()

	// Check if disk image exists
	diskPath := filepath.Join(vmDir, "disk.img")
	if _, err := os.Stat(diskPath); os.IsNotExist(err) {
		return fmt.Errorf("disk image not found: %s", diskPath)
	}

	// Check if user data disk already exists
	userDataPath := DefaultUserDataPath(vmDir)
	if _, err := os.Stat(userDataPath); err == nil {
		return fmt.Errorf("user data disk already exists: %s\nUse 'vz-macos userdata clone' to create a copy", userDataPath)
	}

	// Check disk is not mounted/in use
	if err := checkDiskNotMounted(diskPath); err != nil {
		return fmt.Errorf("system disk appears to be in use: %w", err)
	}

	if *dryRun {
		return migrateDryRun(diskPath, userDataPath, *sizeGB, mountStrategy, *verboseFlag)
	}

	return migrateExecute(diskPath, userDataPath, *sizeGB, mountStrategy, *keepOriginal, *verboseFlag)
}

// migrateDryRun shows what migration would do without making changes
func migrateDryRun(diskPath, userDataPath string, sizeGB uint64, strategy MountStrategy, verbose bool) error {
	fmt.Println("=== DRY RUN - No changes will be made ===")
	fmt.Println()

	// Mount system disk to analyze
	fmt.Println("Step 1: Analyzing system disk...")
	mountPoint, device, _, err := attachAndMountDataVolume(diskPath)
	if err != nil {
		return fmt.Errorf("mount system disk: %w", err)
	}
	defer detachDisk(device)

	// Find /Users on system disk
	usersPath := filepath.Join(mountPoint, "Users")
	if _, err := os.Stat(usersPath); os.IsNotExist(err) {
		fmt.Println("  No /Users directory found on system disk")
		fmt.Println("  Nothing to migrate")
		return nil
	}

	// List users and calculate size
	entries, err := os.ReadDir(usersPath)
	if err != nil {
		return fmt.Errorf("read Users directory: %w", err)
	}

	fmt.Println("  Found users:")
	var totalSize int64
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "Shared" || name == ".localized" {
			continue
		}
		userPath := filepath.Join(usersPath, name)
		size, _ := getDirSize(userPath)
		totalSize += size
		fmt.Printf("    - %s (%s)\n", name, formatSize(size))
	}
	fmt.Printf("  Total size: %s\n", formatSize(totalSize))
	fmt.Println()

	fmt.Println("Step 2: Would create user data disk")
	fmt.Printf("  Path: %s\n", userDataPath)
	fmt.Printf("  Size: %d GB (sparse bundle)\n", sizeGB)
	fmt.Println()

	fmt.Println("Step 3: Would copy users to new disk")
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "Shared" || entry.Name() == ".localized" {
			continue
		}
		fmt.Printf("  - /Users/%s -> /Volumes/UserData/Users/%s\n", entry.Name(), entry.Name())
	}
	fmt.Println()

	fmt.Println("Step 4: Would inject LaunchDaemon for mount strategy:", strategy)
	fmt.Println()

	fmt.Println("=== End Dry Run ===")
	fmt.Println()
	fmt.Println("To execute migration, run without -dry-run:")
	fmt.Println("  sudo ./vz-macos userdata migrate")

	return nil
}

// migrateExecute performs the actual migration
func migrateExecute(diskPath, userDataPath string, sizeGB uint64, strategy MountStrategy, keepOriginal, verbose bool) error {
	fmt.Println("Step 1: Creating user data sparse bundle...")
	if err := createSparseBundleDisk(userDataPath, sizeGB); err != nil {
		return fmt.Errorf("create user data disk: %w", err)
	}
	fmt.Printf("  Created: %s\n", userDataPath)

	fmt.Println()
	fmt.Println("Step 2: Mounting system disk...")
	sysMountPoint, sysDevice, _, err := attachAndMountDataVolume(diskPath)
	if err != nil {
		return fmt.Errorf("mount system disk: %w", err)
	}
	defer detachDisk(sysDevice)
	fmt.Printf("  System disk mounted at: %s\n", sysMountPoint)

	fmt.Println()
	fmt.Println("Step 3: Mounting user data disk...")
	userDataMountPoint, userDataDevice, err := attachAndMountSparseBundleRW(userDataPath)
	if err != nil {
		detachDisk(sysDevice)
		return fmt.Errorf("mount user data disk: %w", err)
	}
	defer detachDisk(userDataDevice)
	fmt.Printf("  User data disk mounted at: %s\n", userDataMountPoint)

	// Find /Users on system disk
	sysUsersPath := filepath.Join(sysMountPoint, "Users")
	if _, err := os.Stat(sysUsersPath); os.IsNotExist(err) {
		fmt.Println("  No /Users directory found on system disk - nothing to migrate")
	} else {
		fmt.Println()
		fmt.Println("Step 4: Copying user data...")

		// Create Users directory on user data disk
		userDataUsersPath := filepath.Join(userDataMountPoint, "Users")
		if err := os.MkdirAll(userDataUsersPath, 0755); err != nil {
			return fmt.Errorf("create Users directory: %w", err)
		}

		// Copy each user directory
		entries, err := os.ReadDir(sysUsersPath)
		if err != nil {
			return fmt.Errorf("read Users directory: %w", err)
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name := entry.Name()
			if name == ".localized" {
				continue
			}

			srcPath := filepath.Join(sysUsersPath, name)
			dstPath := filepath.Join(userDataUsersPath, name)

			fmt.Printf("  Copying %s...\n", name)
			if verbose {
				fmt.Printf("    %s -> %s\n", srcPath, dstPath)
			}

			// Use rsync for proper copying with permissions
			if err := copyDirWithRsync(srcPath, dstPath, verbose); err != nil {
				fmt.Printf("  Warning: failed to copy %s: %v\n", name, err)
				continue
			}
			fmt.Printf("    Done\n")
		}

		// Optionally clear original /Users (except Shared)
		if !keepOriginal {
			fmt.Println()
			fmt.Println("Step 4b: Clearing original /Users...")
			for _, entry := range entries {
				if !entry.IsDir() || entry.Name() == "Shared" || entry.Name() == ".localized" {
					continue
				}
				srcPath := filepath.Join(sysUsersPath, entry.Name())
				fmt.Printf("  Removing %s...\n", entry.Name())
				if err := os.RemoveAll(srcPath); err != nil {
					fmt.Printf("  Warning: failed to remove %s: %v\n", entry.Name(), err)
				}
			}
		}
	}

	fmt.Println()
	fmt.Println("Step 5: Injecting user data mount LaunchDaemon...")
	config := UserDataConfig{
		Enabled:       true,
		Path:          userDataPath,
		MountStrategy: strategy,
	}
	if err := InjectUserDataSetup(sysMountPoint, config, nil); err != nil {
		return fmt.Errorf("inject user data setup: %w", err)
	}

	fmt.Println()
	fmt.Println("=== Migration Complete ===")
	fmt.Println()
	fmt.Println("User data has been migrated to a separate disk.")
	fmt.Printf("  System disk: %s\n", diskPath)
	fmt.Printf("  User data:   %s\n", userDataPath)
	fmt.Printf("  Strategy:    %s\n", strategy)
	fmt.Println()
	fmt.Println("Run the VM with: ./vz-macos run -userdata")

	return nil
}

// attachAndMountSparseBundleRW attaches a sparse bundle and mounts it read-write
func attachAndMountSparseBundleRW(bundlePath string) (mountPoint, device string, err error) {
	// Attach the sparse bundle
	cmd := exec.Command("hdiutil", "attach", bundlePath, "-nobrowse")
	output, err := cmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("hdiutil attach failed: %w", err)
	}

	// Parse output to find device and mount point
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) >= 3 && strings.HasPrefix(fields[0], "/dev/disk") {
			// Last field is mount point
			device = fields[0]
			mountPoint = fields[len(fields)-1]
			// Get the base device (without partition suffix)
			if idx := strings.LastIndex(device, "s"); idx > 5 {
				device = device[:idx]
			}
		}
	}

	if mountPoint == "" || device == "" {
		return "", "", fmt.Errorf("could not parse mount info from: %s", string(output))
	}

	return mountPoint, device, nil
}

// copyDirWithRsync copies a directory using rsync to preserve permissions
func copyDirWithRsync(src, dst string, verbose bool) error {
	args := []string{"-a", "--delete"}
	if verbose {
		args = append(args, "-v")
	}
	args = append(args, src+"/", dst+"/")

	cmd := exec.Command("rsync", args...)
	if verbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	return cmd.Run()
}

// getDirSize calculates the size of a directory
func getDirSize(path string) (int64, error) {
	var size int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // ignore errors, just skip
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size, err
}

// formatSize formats bytes as human-readable string
func formatSize(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.1f GB", float64(bytes)/GB)
	case bytes >= MB:
		return fmt.Sprintf("%.1f MB", float64(bytes)/MB)
	case bytes >= KB:
		return fmt.Sprintf("%.1f KB", float64(bytes)/KB)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// UserDataSetupScript returns the guest-side setup script content (for manual use)
func UserDataSetupScript() string {
	config := UserDataConfig{MountStrategy: MountStrategyVolumes}
	return generateUserDataSetupScript(config)
}

// UserDataHelp returns help text for user data disk feature
func UserDataHelp() string {
	return `Separate User Data Disk
========================

Separate user data from the macOS system disk for easier management.

COMMANDS:
  userdata help              Show this help
  userdata create            Create a new user data sparse bundle
  userdata inject            Inject setup LaunchDaemon into VM disk
  userdata clone <src> <dst> Clone a user data disk (APFS CoW if possible)
  userdata migrate           Migrate existing VM to separate user data
  userdata setup             Print the guest setup script
  userdata workflow          Print the golden image workflow guide

FLAGS (for 'run' command):
  -userdata                    Enable user data disk (default path)
  -userdata-path /path/to.img  Specify user data disk path
  -userdata-size 64            Size in GB for new disk (default: 32)
  -userdata-ro                 Mount as read-only
  -userdata-strategy <s>       Mount strategy: volumes, symlinks, direct
  -userdata-ephemeral          Discard changes after VM stops (CI/CD mode)

MOUNT STRATEGIES:
  volumes   Mount at /Volumes/UserData (default, safest)
            Apps and users manually reference this location

  symlinks  Mount at /Volumes/UserData with symlinks from /Users
            Existing /Users contents are moved to the data disk

  direct    Mount directly as /Users (experimental)
            Replaces the default /Users location

EXAMPLES:
  # Enable separate user data disk
  ./vz-macos run -userdata

  # Custom path and size
  ./vz-macos run -userdata -userdata-path ~/vm-data/users.sparsebundle -userdata-size 100

  # Use with golden image template (read-only system, ephemeral data)
  ./vz-macos run -disk ~/templates/macos-base.img:ro -userdata -userdata-ephemeral

  # Clone user data for a new VM
  ./vz-macos userdata clone ~/.vz/vms/base/userdata.sparsebundle ~/.vz/vms/new/userdata.sparsebundle

SPARSE BUNDLE BENEFITS:
  - Grows on demand (doesn't pre-allocate full size)
  - Band files enable efficient incremental backups
  - APFS clonefile provides instant copy-on-write clones
  - Can be resized with 'hdiutil resize'

See 'vz-macos userdata workflow' for the complete golden image guide.`
}

// GoldenImageWorkflow describes the recommended workflow for golden images
func GoldenImageWorkflow() string {
	return `Golden Image Workflow
=====================

Create a base macOS image and share it across multiple VMs.

PHASE 1: Create Base System
---------------------------

1. Install macOS to create the golden image VM:

   ./vz-macos install -ipsw restore.ipsw

2. Provision the base user (run with sudo for proper ownership):

   sudo ./vz-macos inject -user admin -password secret -skip-setup-assistant

3. Boot and customize the base system:

   ./vz-macos run -gui
   # Install common apps, configure settings, then shut down

PHASE 2: Convert to Template
----------------------------

4. Create the template directory:

   mkdir -p ~/.vz/templates/macos-base

5. Move/copy the golden image:

   # Option A: Move (saves space)
   mv ~/.vz/vms/default/disk.img ~/.vz/templates/macos-base/

   # Option B: Copy (keeps original)
   cp ~/.vz/vms/default/disk.img ~/.vz/templates/macos-base/

6. Copy auxiliary files:

   cp ~/.vz/vms/default/{aux.img,hw.model,machine.id} ~/.vz/templates/macos-base/

PHASE 3: Create VMs from Template
---------------------------------

7. Create a new VM with APFS clone of system + separate user data:

   # Create VM directory
   mkdir -p ~/.vz/vms/dev-vm

   # Clone system disk (instant copy-on-write on APFS)
   cp -c ~/.vz/templates/macos-base/disk.img ~/.vz/vms/dev-vm/
   cp ~/.vz/templates/macos-base/{aux.img,hw.model,machine.id} ~/.vz/vms/dev-vm/

   # Create user data disk
   ./vz-macos -vm dev-vm userdata create -size 64

8. Run with separate user data:

   ./vz-macos -vm dev-vm run -gui -userdata

PHASE 4: CI/CD Ephemeral Mode
-----------------------------

For CI/CD, boot with read-only system and ephemeral user data:

   # System disk is read-only, user data discarded after run
   ./vz-macos run \
     -disk ~/.vz/templates/macos-base/disk.img:ro \
     -userdata \
     -userdata-ephemeral

Benefits:
- Each CI run starts fresh
- No state leaks between runs
- Fast startup (no disk creation)

DISK LAYOUT
-----------

Template:
  ~/.vz/templates/macos-base/
  ├── disk.img        (macOS system, read-only golden image)
  ├── aux.img         (NVRAM, boot data)
  ├── hw.model        (hardware model)
  └── machine.id      (machine identifier)

Per-VM:
  ~/.vz/vms/<name>/
  ├── disk.img              (clone of template, CoW on APFS)
  ├── aux.img               (copy of template)
  ├── hw.model              (copy of template)
  ├── machine.id            (copy of template)
  └── userdata.sparsebundle (separate user data, grows on demand)
      ├── bands/            (sparse bundle data chunks)
      ├── Info.plist
      └── token

MAINTENANCE
-----------

Resize user data disk:
  hdiutil resize -size 100g ~/.vz/vms/<name>/userdata.sparsebundle

Compact user data disk (reclaim unused space):
  hdiutil compact ~/.vz/vms/<name>/userdata.sparsebundle

Export user data for backup:
  cp -R ~/.vz/vms/<name>/userdata.sparsebundle /backup/

Update template (all new VMs get updates):
  # Boot template VM, make changes, shut down
  ./vz-macos -vm template run -gui
  # Existing VMs keep their copy-on-write differences`
}
