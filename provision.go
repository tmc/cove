// provision.go - Auto-provisioning infrastructure for macOS VMs
//
// # Overview
//
// This file implements automatic user provisioning for macOS virtual machines.
// The goal is to achieve fully non-interactive provisioning:
//
//	Boot → User created → Auto-login → Desktop ready
//
// # Architecture
//
// Provisioning works by injecting files directly into the VM's disk image before
// first boot. This approach is more reliable than GUI automation because it
// operates at the filesystem level and doesn't depend on screen detection.
//
// The injection process mounts the VM's APFS "Data" volume and writes:
//
//  1. LaunchDaemon plist (/Library/LaunchDaemons/com.github.tmc.vz-macos.provision.plist)
//  2. Provisioning script (/var/db/vz-provision.sh)
//  3. Setup Assistant bypass marker (/var/db/.AppleSetupDone)
//  4. Auto-login credentials (/etc/kcpassword + loginwindow.plist)
//
// # Boot Sequence
//
// When the VM boots, macOS processes these files in order:
//
//	Kernel → launchd → LaunchDaemons → WindowServer → loginwindow → Desktop
//	                       ↑                              ↑
//	               provision script runs          checks .AppleSetupDone
//	               creates user account           skips Setup Assistant
//
// # LaunchDaemon Provisioning
//
// The LaunchDaemon (com.github.tmc.vz-macos.provision.plist) is configured with RunAtLoad=true,
// which causes launchd to execute the provisioning script immediately at boot.
// The script uses sysadminctl to create the user account with proper credentials.
//
// CRITICAL: LaunchDaemon plists must be owned by root:wheel (uid=0, gid=0).
// macOS launchd silently ignores daemons with incorrect ownership as a security
// measure. The provision command handles this by writing files as the current user,
// then running a single targeted "sudo chown root:wheel" on just the files that
// need it — minimizing the scope of elevated privileges.
//
// # APFS Volume Handling
//
// macOS VMs use APFS with multiple volumes. The user data lives on the "Data"
// volume, which is separate from the system volume. When mounting disk images:
//
//  1. hdiutil attach creates device nodes (e.g., /dev/disk27)
//  2. APFS containers synthesize additional devices (e.g., /dev/disk30)
//  3. The Data volume may mount at /Volumes/Data, /Volumes/Data 1, etc.
//  4. diskutil info must be used to find the actual mount point
//
// IMPORTANT: APFS volumes from disk images have ownership disabled by default.
// The provision command calls "diskutil enableOwnership" before writing files,
// otherwise chown operations silently do nothing.
//
// # Setup Assistant Bypass
//
// The .AppleSetupDone marker file tells loginwindow to skip Setup Assistant.
// This file is checked at:
//
//	/var/db/.AppleSetupDone (guest path)
//	/Volumes/Data/private/var/db/.AppleSetupDone (host path when mounted)
//
// Additional bypass mechanisms:
//   - .skipbuddy in /Library/User Template/English.lproj/ suppresses first-login dialogs
//   - .SetupRegComplete in /Library/Receipts/ (older macOS versions)
//
// # Auto-Login Configuration
//
// Auto-login requires two files working together:
//
//  1. kcpassword (/etc/kcpassword): XOR-encoded password using a fixed 11-byte key
//  2. loginwindow.plist: Contains autoLoginUser key with the username
//
// The kcpassword encoding is intentionally weak (security through obscurity)
// because auto-login inherently requires storing credentials. See password.go
// for the encoding implementation.
//
// # Usage
//
// Complete provisioning workflow:
//
//	# Install macOS to create VM
//	./vz-macos install -ipsw restore.ipsw
//
//	# Provision the VM (will prompt for sudo to fix file ownership)
//	./vz-macos provision -user testuser -password secret123 -skip-setup-assistant
//
//	# Verify provisioning succeeded
//	./vz-macos verify
//
//	# Run VM - should boot directly to desktop
//	./vz-macos run -gui
//
// # Troubleshooting
//
// Common issues and solutions:
//
//	WRONG_OWNER verification error:
//	  → Re-run provision (will prompt for sudo to fix ownership)
//	  → Ensure no other Data volumes are mounted
//
//	User not created on boot:
//	  → Check /var/log/vz-provision.log inside VM
//	  → Verify LaunchDaemon has root:wheel ownership
//
//	Setup Assistant still appears:
//	  → Ensure -skip-setup-assistant flag was used
//	  → Check .AppleSetupDone exists with correct path
//
//	Auto-login not working:
//	  → May be disabled by FileVault
//	  → Check kcpassword encoding matches password
//
// # File Path Mapping
//
// When the VM disk is mounted on the host, paths map as follows:
//
//	Guest Path              → Host Path (on Data volume)
//	/var/db/                → /Volumes/Data/private/var/db/
//	/Library/               → /Volumes/Data/Library/
//	/Users/                 → /Volumes/Data/Users/
//	/etc/                   → /Volumes/Data/private/etc/
//
// # File Organization
//
// The provisioning subsystem is split across multiple files:
//
//	provision.go             - Types, staging utilities, orchestration
//	provision_cli.go         - CLI handler, InjectOptions, password prompts
//	provision_mount.go       - APFS disk mount, partition discovery, ownership
//	provision_inject.go      - File staging: LaunchDaemon, auto-login, user plist, SSH keys
//	provision_verify.go      - File verification command
//	provision_automation.go  - Setup Assistant keyboard automation
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// ProvisionManifest describes staged provisioning files for the apply phase.
type ProvisionManifest struct {
	Version int                     `json:"version"`
	VMDir   string                  `json:"vmDir"`
	Created time.Time               `json:"created"`
	Files   []ProvisionManifestFile `json:"files"`
}

// ProvisionManifestFile describes a single staged file and its target ownership.
type ProvisionManifestFile struct {
	Path  string `json:"path"`  // relative to Data volume mount point
	Mode  string `json:"mode"`  // octal file mode, e.g. "0755"
	Owner string `json:"owner"` // "root:wheel" or "" for default
}

// provisionStagingDir returns the staging directory for the current VM.
func provisionStagingDir() string {
	return filepath.Join(vmDir, ".provision")
}

// stageFile writes data to the staging directory and appends a manifest entry.
func stageFile(stagingDir, relativePath string, data []byte, mode os.FileMode, owner string, manifest *ProvisionManifest) error {
	dest := filepath.Join(stagingDir, relativePath)
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return fmt.Errorf("create staging directory for %s: %w", relativePath, err)
	}
	if err := os.WriteFile(dest, data, mode); err != nil {
		return fmt.Errorf("write staged file %s: %w", relativePath, err)
	}
	manifest.Files = append(manifest.Files, ProvisionManifestFile{
		Path:  relativePath,
		Mode:  fmt.Sprintf("0%o", mode),
		Owner: owner,
	})
	fmt.Printf("  Staged: %s\n", relativePath)
	return nil
}

// writeManifest writes the provision manifest to the staging directory.
func writeManifest(stagingDir string, manifest *ProvisionManifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	return os.WriteFile(filepath.Join(stagingDir, "manifest.json"), data, 0644)
}

// readManifest reads a provision manifest from the staging directory.
func readManifest(stagingDir string) (*ProvisionManifest, error) {
	data, err := os.ReadFile(filepath.Join(stagingDir, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m ProvisionManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	return &m, nil
}

// ProvisionConfig holds the user provisioning configuration
type ProvisionConfig struct {
	Username          string `json:"username"`
	Password          string `json:"password"`
	Fullname          string `json:"fullname,omitempty"`
	Admin             bool   `json:"admin"`
	BootstrapRecovery bool   `json:"bootstrap_recovery,omitempty"` // Two-user bootstrap for recovery auth
	InstallXcodeCLI   bool   `json:"install_xcode_cli,omitempty"`  // Optionally install Xcode Command Line Tools
	EnableSSHD        bool   `json:"enable_sshd,omitempty"`        // Enable SSH daemon (Remote Login) on first boot
}

// cleanVM removes all VM files from vmDir
func cleanVM() error {
	fmt.Printf("Cleaning VM directory: %s\n", vmDir)

	files := []string{
		"disk.img",
		"aux.img",
		"hw.model",
		"machine.id",
		"boot-args.txt",
	}

	for _, f := range files {
		path := filepath.Join(vmDir, f)
		if _, err := os.Stat(path); err == nil {
			if err := os.Remove(path); err != nil {
				fmt.Printf("warning: could not remove %s: %v\n", path, err)
			} else {
				fmt.Printf("Removed: %s\n", path)
			}
		}
	}

	fmt.Println("VM cleaned.")
	return nil
}

// injectProvisioningFilesWithOptions mounts the VM disk and injects provisioning files
// with full configuration options including auto-login and direct user plist creation.
func injectProvisioningFilesWithOptions(opts InjectOptions) error {
	// Validate username
	if err := validateUsername(opts.Config.Username); err != nil {
		return fmt.Errorf("invalid username: %w", err)
	}

	// Check password is not empty
	if opts.Config.Password == "" {
		return fmt.Errorf("password cannot be empty")
	}

	if err := checkVMNotRunning(); err != nil {
		return err
	}

	diskPath := filepath.Join(vmDir, "disk.img")
	if _, err := os.Stat(diskPath); os.IsNotExist(err) {
		return fmt.Errorf("disk image not found: %s\nRun 'vz-macos install' first to create a VM", diskPath)
	}

	// Check disk is not already mounted
	if err := checkDiskNotMounted(diskPath); err != nil {
		return err
	}

	fmt.Println("=== Injecting Provisioning Files ===")
	fmt.Printf("Username: %s\n", opts.Config.Username)
	fmt.Printf("Admin: %v\n", opts.Config.Admin)
	fmt.Printf("Skip Setup Assistant: %v\n", opts.SkipSetupAssistant)
	if opts.SkipSetupAssistant && !opts.Config.BootstrapRecovery {
		fmt.Println("  Note: Setup Assistant bypassed without bootstrap recovery.")
		fmt.Println("  The provision script will run 'diskutil apfs updatePreboot /'")
		fmt.Println("  but recovery auth may not work. Consider using -bootstrap-recovery.")
	}
	fmt.Printf("Auto-Login: %v\n", opts.AutoLogin)
	fmt.Printf("Create User Plist: %v\n", opts.CreateUserPlist)
	fmt.Printf("Guest Tools: %v\n", opts.InjectGuestTools)
	fmt.Printf("Enable SSHD: %v\n", opts.EnableSSHD)
	if opts.SSHKeyPath != "" {
		fmt.Printf("SSH Key: %s\n", opts.SSHKeyPath)
	}
	fmt.Println()

	// Step 1: Attach and mount the Data volume
	mountPoint, device, dataPart, err := attachAndMountDataVolume(diskPath)
	if err != nil {
		return fmt.Errorf("mount data volume: %w", err)
	}
	defer detachDisk(device)

	// Ensure disk is detached on Ctrl+C / SIGTERM. Without this, a
	// mid-inject interrupt leaves the disk attached, causing "storage
	// device attachment is invalid" on the next `run`.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	interrupted := make(chan struct{})
	go func() {
		<-sigCh
		fmt.Fprintln(os.Stderr, "\ninterrupted — detaching disk before exit...")
		close(interrupted)
	}()
	defer signal.Stop(sigCh)
	isInterrupted := func() bool {
		select {
		case <-interrupted:
			return true
		default:
			return false
		}
	}

	// Collect files that need root:wheel ownership. If we're not running as root,
	// os.Chown will fail silently and paths accumulate here. At the end we run a
	// single targeted "sudo chown root:wheel <files>" so only that one command
	// needs elevated privileges.
	var rootFiles []string
	// Collect files that need to be copied to root-owned directories
	// (e.g. /usr/local/bin which is owned by root). These are staged to temp
	// and installed via the elevated script.
	var pendingInstalls []pendingInstall

	if isInterrupted() {
		return fmt.Errorf("interrupted")
	}

	if opts.CreateUserPlist {
		// Advanced mode: Create user plist directly with password hash
		if err := injectUserPlist(mountPoint, opts); err != nil {
			return fmt.Errorf("inject user plist: %w", err)
		}
	} else {
		// Standard mode: Use LaunchDaemon to run sysadminctl on first boot
		if err := injectLaunchDaemonProvisioning(mountPoint, opts.Config, &rootFiles); err != nil {
			return fmt.Errorf("inject LaunchDaemon: %w", err)
		}
	}

	// Optionally create .AppleSetupDone to skip Setup Assistant
	if opts.SkipSetupAssistant {
		// Create .AppleSetupDone to skip main Setup Assistant
		setupDonePath := filepath.Join(mountPoint, "private", "var", "db", ".AppleSetupDone")
		if err := os.WriteFile(setupDonePath, []byte{}, 0644); err != nil {
			fmt.Printf("warning: could not create .AppleSetupDone: %v\n", err)
		} else {
			fmt.Printf("Written: %s\n", setupDonePath)
		}

		// Create .skipbuddy in User Template to suppress first-login dialogs
		// (iCloud setup, Siri setup, etc.)
		userTemplateDir := filepath.Join(mountPoint, "Library", "User Template", "English.lproj")
		if err := os.MkdirAll(userTemplateDir, 0755); err != nil {
			provisionLog("warning: could not create User Template directory: %v", err)
		} else {
			skipBuddyPath := filepath.Join(userTemplateDir, ".skipbuddy")
			if err := os.WriteFile(skipBuddyPath, []byte{}, 0644); err != nil {
				provisionLog("warning: could not create .skipbuddy: %v", err)
			} else {
				provisionLog("Written: %s (suppresses first-login dialogs)", skipBuddyPath)
			}
		}
	}

	// Optionally configure auto-login
	if opts.AutoLogin {
		if err := injectAutoLogin(mountPoint, opts.Config.Username, opts.Config.Password, &rootFiles); err != nil {
			fmt.Printf("warning: auto-login configuration failed: %v\n", err)
		}
	}

	// Optionally inject SSH key for remote access
	if opts.SSHKeyPath != "" {
		if err := injectSSHKey(mountPoint, opts.Config.Username, opts.SSHKeyPath); err != nil {
			fmt.Printf("warning: SSH key injection failed: %v\n", err)
		}
	}

	// Optionally cross-compile and inject the vz-agent GRPC daemon
	if opts.InjectAgent {
		if err := injectAgent(mountPoint, &rootFiles, &pendingInstalls); err != nil {
			return fmt.Errorf("inject agent: %w", err)
		}
	}

	// Optionally download and inject SPICE guest tools for clipboard sharing
	if opts.InjectGuestTools {
		if err := injectGuestTools(mountPoint, &rootFiles); err != nil {
			fmt.Printf("warning: guest tools injection failed: %v\n", err)
			fmt.Println("  Clipboard sharing will not work until guest tools are installed manually.")
		}
	}

	if isInterrupted() {
		return fmt.Errorf("interrupted")
	}

	// Fix ownership on files that need root:wheel, and copy any files
	// that were staged because their target directories are root-owned.
	// If already running as root, both lists will be empty.
	if err := fixOwnershipWithSudo(rootFiles, dataPart, pendingInstalls...); err != nil {
		if strings.Contains(err.Error(), "interrupted") {
			fmt.Printf("\n%v\n", err)
			return err // defer detachDisk will run
		}
		fmt.Printf("warning: could not fix file ownership: %v\n", err)
		fmt.Println("  LaunchDaemons may not load on first boot.")
		fmt.Println("  Fix manually with: sudo chown root:wheel <files>")
	}
	// Clean up temp files from pending installs.
	for _, inst := range pendingInstalls {
		os.Remove(inst.Src)
	}

	fmt.Println()
	fmt.Println("=== Injection Complete ===")
	if opts.CreateUserPlist {
		fmt.Printf("User '%s' created directly in user database.\n", opts.Config.Username)
	} else {
		fmt.Println("On first boot, the provisioning script will:")
		fmt.Printf("  - Create user '%s'\n", opts.Config.Username)
		if opts.Config.Admin {
			fmt.Println("  - Grant admin privileges")
		}
		fmt.Println("  - Self-cleanup (remove script and LaunchDaemon)")
	}
	if opts.SkipSetupAssistant {
		fmt.Println("  - Skip Setup Assistant entirely")
	}
	if opts.AutoLogin {
		fmt.Printf("  - Auto-login as '%s'\n", opts.Config.Username)
	}
	if opts.SSHKeyPath != "" {
		fmt.Printf("  - SSH key added to %s's authorized_keys\n", opts.Config.Username)
	}
	if opts.InjectAgent {
		fmt.Println("  - vz-agent GRPC daemon installed (vsock port 1024)")
	}
	if opts.InjectGuestTools {
		fmt.Println("  - SPICE guest tools (clipboard sharing) will install on first boot")
	}
	if opts.Config.BootstrapRecovery {
		fmt.Println("  - Two-user bootstrap: hidden admin created first for recovery auth")
	}
	if opts.EnableSSHD {
		fmt.Println("  - SSH daemon (Remote Login) will be enabled on first boot")
	}
	fmt.Println()

	fmt.Println("Run the VM with: ./vz-macos run")

	return nil
}

// stageProvisioningFiles performs all expensive operations (builds, downloads,
// file generation) and writes them to a staging directory. No root access needed.
// The staged files can then be applied to the disk with applyProvisioningFiles.
func stageProvisioningFiles(opts InjectOptions) (string, error) {
	// Validate username
	if err := validateUsername(opts.Config.Username); err != nil {
		return "", fmt.Errorf("invalid username: %w", err)
	}
	if opts.Config.Password == "" {
		return "", fmt.Errorf("password cannot be empty")
	}

	diskPath := filepath.Join(vmDir, "disk.img")
	if _, err := os.Stat(diskPath); os.IsNotExist(err) {
		return "", fmt.Errorf("disk image not found: %s\nRun 'vz-macos install' first to create a VM", diskPath)
	}

	stagingDir := provisionStagingDir()

	// Clean any previous staging directory.
	os.RemoveAll(stagingDir)
	if err := os.MkdirAll(stagingDir, 0755); err != nil {
		return "", fmt.Errorf("create staging directory: %w", err)
	}

	manifest := &ProvisionManifest{
		Version: 1,
		VMDir:   vmDir,
		Created: time.Now(),
	}

	fmt.Println("=== Staging Provisioning Files ===")
	fmt.Printf("Username: %s\n", opts.Config.Username)
	fmt.Printf("Admin: %v\n", opts.Config.Admin)
	fmt.Printf("Skip Setup Assistant: %v\n", opts.SkipSetupAssistant)
	if opts.SkipSetupAssistant && !opts.Config.BootstrapRecovery {
		fmt.Println("  Note: Setup Assistant bypassed without bootstrap recovery.")
		fmt.Println("  The provision script will run 'diskutil apfs updatePreboot /'")
		fmt.Println("  but recovery auth may not work. Consider using -bootstrap-recovery.")
	}
	fmt.Printf("Auto-Login: %v\n", opts.AutoLogin)
	fmt.Printf("Create User Plist: %v\n", opts.CreateUserPlist)
	fmt.Printf("Guest Tools: %v\n", opts.InjectGuestTools)
	fmt.Printf("Enable SSHD: %v\n", opts.EnableSSHD)
	if opts.SSHKeyPath != "" {
		fmt.Printf("SSH Key: %s\n", opts.SSHKeyPath)
	}
	fmt.Println()

	// Stage LaunchDaemon provisioning (or user plist).
	if opts.CreateUserPlist {
		if err := stageUserPlist(stagingDir, opts, manifest); err != nil {
			return "", fmt.Errorf("stage user plist: %w", err)
		}
	} else {
		if err := stageLaunchDaemonProvisioning(stagingDir, opts.Config, manifest); err != nil {
			return "", fmt.Errorf("stage LaunchDaemon: %w", err)
		}
	}

	// Stage .AppleSetupDone and .skipbuddy.
	if opts.SkipSetupAssistant {
		if err := stageFile(stagingDir, filepath.Join("private", "var", "db", ".AppleSetupDone"),
			[]byte{}, 0644, "", manifest); err != nil {
			fmt.Printf("warning: could not stage .AppleSetupDone: %v\n", err)
		}
		if err := stageFile(stagingDir, filepath.Join("Library", "User Template", "English.lproj", ".skipbuddy"),
			[]byte{}, 0644, "", manifest); err != nil {
			provisionLog("warning: could not stage .skipbuddy: %v", err)
		}
	}

	// Stage auto-login files.
	if opts.AutoLogin {
		if err := stageAutoLogin(stagingDir, opts.Config.Username, opts.Config.Password, manifest); err != nil {
			fmt.Printf("warning: auto-login staging failed: %v\n", err)
		}
	}

	// Stage SSH key.
	if opts.SSHKeyPath != "" {
		if err := stageSSHKey(stagingDir, opts.Config.Username, opts.SSHKeyPath, manifest); err != nil {
			fmt.Printf("warning: SSH key staging failed: %v\n", err)
		}
	}

	// Stage vz-agent binary (cross-compile happens here, no root needed).
	if opts.InjectAgent {
		if err := stageAgent(stagingDir, manifest); err != nil {
			return "", fmt.Errorf("stage agent: %w", err)
		}
	}

	// Stage guest tools (download happens here, no root needed).
	if opts.InjectGuestTools {
		if err := stageGuestTools(stagingDir, manifest); err != nil {
			fmt.Printf("warning: guest tools staging failed: %v\n", err)
			fmt.Println("  Clipboard sharing will not work until guest tools are installed manually.")
		}
	}

	// Write the manifest.
	if err := writeManifest(stagingDir, manifest); err != nil {
		return "", fmt.Errorf("write manifest: %w", err)
	}

	fmt.Printf("\nStaged %d file(s) to %s\n", len(manifest.Files), stagingDir)
	return stagingDir, nil
}

// applyProvisioningFiles reads a staging manifest, mounts the VM disk, and
// copies all staged files to the Data volume with correct ownership.
// This is the only step that may require elevated privileges (via osascript).
func applyProvisioningFiles() error {
	stagingDir := provisionStagingDir()

	manifest, err := readManifest(stagingDir)
	if err != nil {
		return fmt.Errorf("no staged provisioning files found: %w\n  run 'vz-macos inject -user <username> -skip-setup-assistant' to stage and apply", err)
	}

	diskPath := filepath.Join(vmDir, "disk.img")
	if _, err := os.Stat(diskPath); os.IsNotExist(err) {
		return fmt.Errorf("disk image not found: %s", diskPath)
	}

	if err := checkDiskNotMounted(diskPath); err != nil {
		return err
	}

	fmt.Println("=== Applying Provisioning Files ===")
	fmt.Printf("VM: %s\n", vmDir)
	fmt.Printf("Files to apply: %d\n\n", len(manifest.Files))

	// Mount the Data volume.
	mountPoint, device, dataPart, err := attachAndMountDataVolume(diskPath)
	if err != nil {
		return fmt.Errorf("mount data volume: %w", err)
	}
	defer detachDisk(device)

	// Handle Ctrl+C — close a channel instead of os.Exit so deferred
	// detachDisk runs and the disk is cleanly released.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	interrupted := make(chan struct{})
	go func() {
		<-sigCh
		fmt.Fprintln(os.Stderr, "\ninterrupted — detaching disk before exit...")
		close(interrupted)
	}()
	defer signal.Stop(sigCh)

	select {
	case <-interrupted:
		return fmt.Errorf("interrupted")
	default:
	}

	// Build a shell script that enables ownership, copies files, sets modes
	// and ownership in one elevated pass. This avoids the problem where APFS
	// ownership is disabled (default for disk images) and we can't even
	// create files in system directories without root.
	if err := applyStagedFiles(stagingDir, mountPoint, dataPart, manifest); err != nil {
		return err
	}

	// Clean up staging directory on success.
	os.RemoveAll(stagingDir)
	markInjectSucceeded()

	fmt.Println()
	fmt.Println("=== Provisioning Applied Successfully ===")
	fmt.Println("Run the VM with: ./vz-macos run")
	return nil
}

// applyStagedFiles enables APFS ownership, copies all staged files to the
// mount point, sets permissions and ownership. If not running as root, the
// entire operation runs via a single osascript elevation prompt.
func applyStagedFiles(stagingDir, mountPoint, dataPart string, manifest *ProvisionManifest) error {
	// Build a shell script that does everything in one elevated pass:
	// 1. Enable APFS ownership on the partition
	// 2. Create directories
	// 3. Copy each file
	// 4. Set mode and ownership
	var script strings.Builder
	script.WriteString("#!/bin/bash\n")
	script.WriteString("set -e\n")
	script.WriteString(fmt.Sprintf("diskutil enableOwnership %s >/dev/null 2>&1\n", dataPart))

	for _, f := range manifest.Files {
		src := filepath.Join(stagingDir, f.Path)
		dst := filepath.Join(mountPoint, f.Path)

		script.WriteString(fmt.Sprintf("mkdir -p %q\n", filepath.Dir(dst)))
		script.WriteString(fmt.Sprintf("cp %q %q\n", src, dst))
		if f.Mode != "" {
			script.WriteString(fmt.Sprintf("chmod %s %q\n", f.Mode, dst))
		}
		if f.Owner == "root:wheel" {
			script.WriteString(fmt.Sprintf("chown root:wheel %q\n", dst))
		}
	}

	// Write the script to a temp file so osascript gets a short one-liner.
	// Passing large inline scripts to "do shell script" causes osascript to
	// spin indefinitely after the script exits (known macOS bug with the
	// authorization session cleanup in "with administrator privileges").
	tmpScript, err := os.CreateTemp("", "vz-provision-apply-*.sh")
	if err != nil {
		return fmt.Errorf("create temp script: %w", err)
	}
	tmpPath := tmpScript.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpScript.WriteString(script.String()); err != nil {
		tmpScript.Close()
		return fmt.Errorf("write temp script: %w", err)
	}
	tmpScript.Close()
	os.Chmod(tmpPath, 0755)

	if os.Getuid() == 0 {
		fmt.Println("Running as root, applying files directly...")
		cmd := exec.Command("bash", tmpPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("apply files: %w", err)
		}
	} else {
		fmt.Println("Requesting macOS host administrator privileges...")
		if err := runElevatedBash(tmpPath); err != nil {
			return err
		}
	}

	for _, f := range manifest.Files {
		fmt.Printf("  Applied: %s\n", f.Path)
	}
	return nil
}

// runElevatedBash runs a bash script with root privileges.
//
// Strategy:
//  1. Try sudo -n (reuse cached credentials).
//  2. Try native AuthorizationServices (system dialog with Touch ID).
//  3. Fall back to password prompt + sudo -S.
func runElevatedBash(scriptPath string) error {
	// Try cached sudo credentials first.
	cmd := exec.Command("sudo", "-n", "bash", scriptPath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		if stdout.Len() > 0 {
			_, _ = os.Stdout.Write(stdout.Bytes())
		}
		if stderr.Len() > 0 {
			_, _ = os.Stderr.Write(stderr.Bytes())
		}
		return nil
	} else if msg := stderr.String(); !strings.Contains(msg, "a password is required") && !strings.Contains(msg, "a terminal is required") {
		if stdout.Len() > 0 {
			_, _ = os.Stdout.Write(stdout.Bytes())
		}
		if stderr.Len() > 0 {
			_, _ = os.Stderr.Write(stderr.Bytes())
		}
		return fmt.Errorf("elevated apply: %w", err)
	}

	// Try native macOS authorization (system dialog with Touch ID).
	if err := runElevatedBashNative(scriptPath); err == nil {
		return nil
	}

	// Fall back to password prompt + sudo -S.
	return runElevatedBashSudo(scriptPath)
}

// runElevatedBashSudo prompts for the admin password and runs via sudo -S.
func runElevatedBashSudo(scriptPath string) error {
	hostUser := os.Getenv("USER")
	if hostUser == "" {
		if u, err := user.Current(); err == nil {
			hostUser = filepath.Base(u.Username)
		}
	}
	prompt := "Host administrator password (for this Mac, not the VM user): "
	if hostUser != "" {
		prompt = fmt.Sprintf("Host administrator password for %s (this Mac, not the VM user): ", hostUser)
	}
	pw, err := readPassword(prompt)
	if err != nil {
		if strings.Contains(err.Error(), "interrupted") || strings.Contains(err.Error(), "canceled") {
			return fmt.Errorf("interrupted: user cancelled authorization")
		}
		return fmt.Errorf("read administrator password: %w", err)
	}

	cmd := exec.Command("sudo", "-S", "-p", "", "bash", scriptPath)
	cmd.Stdin = strings.NewReader(string(pw) + "\n")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("elevated apply: %w", err)
	}
	return nil
}

// stageAgent cross-compiles the vz-agent binary and stages it along with
// its LaunchDaemon plist. The build happens here (no root needed).
func stageAgent(stagingDir string, manifest *ProvisionManifest) error {
	fmt.Println()
	fmt.Println("=== Building Guest Agent ===")

	tmpBinary := filepath.Join(os.TempDir(), agentBinaryName)
	defer os.Remove(tmpBinary)

	if err := buildAgentBinary(tmpBinary); err != nil {
		return err
	}

	binaryData, err := os.ReadFile(tmpBinary)
	if err != nil {
		return fmt.Errorf("read built binary: %w", err)
	}

	if err := stageFile(stagingDir, filepath.Join("usr", "local", "bin", agentBinaryName),
		binaryData, 0755, "root:wheel", manifest); err != nil {
		return err
	}

	if err := stageFile(stagingDir, filepath.Join("Library", "LaunchDaemons", agentLaunchDaemonLabel+".plist"),
		[]byte(agentLaunchDaemonPlist), 0644, "root:wheel", manifest); err != nil {
		return err
	}

	return nil
}

// stageGuestTools downloads the guest tools package and stages it along with
// its installer script and LaunchDaemon. The download happens here (no root needed).
func stageGuestTools(stagingDir string, manifest *ProvisionManifest) error {
	fmt.Println()
	fmt.Println("=== Downloading SPICE Guest Tools ===")

	pkgPath, err := ensureGuestToolsPkg()
	if err != nil {
		return err
	}

	pkgData, err := os.ReadFile(pkgPath)
	if err != nil {
		return fmt.Errorf("read cached pkg: %w", err)
	}

	if err := stageFile(stagingDir, filepath.Join("private", "var", "db", "vz-guest-tools.pkg"),
		pkgData, 0644, "root:wheel", manifest); err != nil {
		return err
	}

	if err := stageFile(stagingDir, filepath.Join("Library", "LaunchDaemons", guestToolsLaunchDaemonLabel+".plist"),
		[]byte(guestToolsLaunchDaemonPlist), 0644, "root:wheel", manifest); err != nil {
		return err
	}

	if err := stageFile(stagingDir, filepath.Join("private", "var", "db", "vz-install-guest-tools.sh"),
		[]byte(guestToolsInstallScript), 0755, "root:wheel", manifest); err != nil {
		return err
	}

	return nil
}
