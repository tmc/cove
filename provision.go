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
//	./cove install -ipsw restore.ipsw
//
//	# Provision the VM (will prompt for sudo to fix file ownership)
//	./cove provision -user testuser -password secret123 -skip-setup-assistant
//
//	# Verify provisioning succeeded
//	./cove verify
//
//	# Run VM - should boot directly to desktop
//	./cove run -gui
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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
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
	return provisionStagingDirForVM(currentVMSelection())
}

func provisionStagingDirForVM(target vmSelection) string {
	return target.provisionStagingDir()
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
	if verbose {
		fmt.Printf("  staged: %s\n", relativePath)
	}
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

// stagingFingerprint summarizes the inputs that determined the staged files.
// If two stages produce the same fingerprint, their staged outputs are
// equivalent — so the second can reuse the first instead of re-staging.
type stagingFingerprint struct {
	Username           string `json:"username"`
	Admin              bool   `json:"admin"`
	BootstrapRecovery  bool   `json:"bootstrapRecovery"`
	SkipSetupAssistant bool   `json:"skipSetupAssistant"`
	AutoLogin          bool   `json:"autoLogin"`
	CreateUserPlist    bool   `json:"createUserPlist"`
	InjectAgent        bool   `json:"injectAgent"`
	InjectGuestTools   bool   `json:"injectGuestTools"`
	EnableSSHD         bool   `json:"enableSSHD"`
	SSHKeyPath         string `json:"sshKeyPath,omitempty"`
}

func makeStagingFingerprint(opts InjectOptions) stagingFingerprint {
	return stagingFingerprint{
		Username:           opts.Config.Username,
		Admin:              opts.Config.Admin,
		BootstrapRecovery:  opts.Config.BootstrapRecovery,
		SkipSetupAssistant: opts.SkipSetupAssistant,
		AutoLogin:          opts.AutoLogin,
		CreateUserPlist:    opts.CreateUserPlist,
		InjectAgent:        opts.InjectAgent,
		InjectGuestTools:   opts.InjectGuestTools,
		EnableSSHD:         opts.EnableSSHD,
		SSHKeyPath:         opts.SSHKeyPath,
	}
}

// writeStagingFingerprint records the fingerprint alongside the manifest so
// later stage-or-reuse decisions can compare without re-deriving it.
func writeStagingFingerprint(stagingDir string, fp stagingFingerprint) error {
	data, err := json.MarshalIndent(fp, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(stagingDir, "fingerprint.json"), data, 0644)
}

// stagingMatchesOptions reports whether stagingDir contains a complete prior
// stage that matches the given options. A matching stage can be reused
// without rebuilding any artifacts.
func stagingMatchesOptions(stagingDir string, opts InjectOptions) (bool, error) {
	if _, err := os.Stat(filepath.Join(stagingDir, "manifest.json")); err != nil {
		return false, err
	}
	data, err := os.ReadFile(filepath.Join(stagingDir, "fingerprint.json"))
	if err != nil {
		return false, err
	}
	var existing stagingFingerprint
	if err := json.Unmarshal(data, &existing); err != nil {
		return false, err
	}
	return existing == makeStagingFingerprint(opts), nil
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
	return cleanVMForVM(currentVMSelection())
}

func cleanVMForVM(target vmSelection) error {
	fmt.Printf("Cleaning VM directory: %s\n", target.Directory)

	files := []string{
		"disk.img",
		"aux.img",
		"hw.model",
		"machine.id",
		"boot-args.txt",
		".inject-succeeded",
	}

	for _, f := range files {
		path := filepath.Join(target.Directory, f)
		if _, err := os.Stat(path); err == nil {
			if err := os.Remove(path); err != nil {
				fmt.Printf("warning: could not remove %s: %v\n", path, err)
			} else {
				fmt.Printf("Removed: %s\n", path)
			}
		}
	}

	// Remove provisioning staging directory.
	stagingDir := provisionStagingDirForVM(target)
	if _, err := os.Stat(stagingDir); err == nil {
		if err := os.RemoveAll(stagingDir); err != nil {
			fmt.Printf("warning: could not remove %s: %v\n", stagingDir, err)
		} else {
			fmt.Printf("Removed: %s\n", stagingDir)
		}
	}

	fmt.Println("VM cleaned.")
	return nil
}

// stageProvisioningFiles performs all expensive operations (builds, downloads,
// file generation) and writes them to a staging directory. No root access needed.
// The staged files can then be applied to the disk with applyProvisioningFiles.
func stageProvisioningFiles(opts InjectOptions) (string, error) {
	return stageProvisioningFilesForVM(currentVMSelection(), opts)
}

func stageProvisioningFilesForVM(target vmSelection, opts InjectOptions) (string, error) {
	// Validate username
	if err := validateUsername(opts.Config.Username); err != nil {
		return "", fmt.Errorf("invalid username: %w", err)
	}
	if opts.Config.Password == "" {
		return "", fmt.Errorf("password cannot be empty")
	}

	diskPath := target.diskPath()
	if _, err := os.Stat(diskPath); os.IsNotExist(err) {
		return "", fmt.Errorf("disk image not found: %s\nRun 'cove install' first to create a VM", diskPath)
	}

	stagingDir := provisionStagingDirForVM(target)

	// If a complete staging directory already exists for the same user, reuse
	// it. This makes provisioning resumable: an interrupted apply can re-run
	// without rebuilding the agent or re-staging unchanged files.
	if reusable, err := stagingMatchesOptions(stagingDir, opts); err == nil && reusable {
		if verbose {
			fmt.Printf("Reusing staged files (delete %s to force re-stage).\n", stagingDir)
		} else {
			fmt.Println("Reusing staged provisioning files.")
		}
		return stagingDir, nil
	}

	// Clean any previous staging directory.
	os.RemoveAll(stagingDir)
	if err := os.MkdirAll(stagingDir, 0755); err != nil {
		return "", fmt.Errorf("create staging directory: %w", err)
	}

	manifest := &ProvisionManifest{
		Version: 1,
		VMDir:   target.Directory,
		Created: time.Now(),
	}

	if verbose {
		fmt.Printf("Staging provisioning for user %q (admin=%v, autologin=%v).\n",
			opts.Config.Username, opts.Config.Admin, opts.AutoLogin)
		fmt.Printf("  skip-setup-assistant=%v create-user-plist=%v guest-tools=%v sshd=%v\n",
			opts.SkipSetupAssistant, opts.CreateUserPlist, opts.InjectGuestTools, opts.EnableSSHD)
		if opts.SSHKeyPath != "" {
			fmt.Printf("  ssh-key=%s\n", opts.SSHKeyPath)
		}
		if opts.SkipSetupAssistant && !opts.Config.BootstrapRecovery {
			fmt.Println("  note: setup-assistant bypassed without bootstrap-recovery (recovery auth may fail)")
		}
	} else {
		fmt.Printf("Staging provisioning for %q...\n", opts.Config.Username)
	}

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
	if err := writeStagingFingerprint(stagingDir, makeStagingFingerprint(opts)); err != nil {
		return "", fmt.Errorf("write fingerprint: %w", err)
	}

	if verbose {
		fmt.Printf("Staged %d files in %s.\n", len(manifest.Files), filepath.Base(stagingDir))
	}
	return stagingDir, nil
}

// applyProvisioningFiles reads a staging manifest, mounts the VM disk, and
// copies all staged files to the Data volume with correct ownership.
// This is the only step that may require elevated privileges.
func applyProvisioningFiles() error {
	return applyProvisioningFilesForVM(currentVMSelection())
}

func applyProvisioningFilesForVM(target vmSelection) error {
	stagingDir := provisionStagingDirForVM(target)

	manifest, err := readManifest(stagingDir)
	if err != nil {
		return fmt.Errorf("no staged provisioning files found: %w\n  run 'cove inject -user <username> -skip-setup-assistant' to stage and apply", err)
	}

	diskPath := target.diskPath()
	if _, err := os.Stat(diskPath); os.IsNotExist(err) {
		return fmt.Errorf("disk image not found: %s", diskPath)
	}

	if err := checkDiskNotMounted(diskPath); err != nil {
		return err
	}

	if verbose {
		fmt.Printf("Applying %d provisioning files to %s.\n", len(manifest.Files), filepath.Base(target.Directory))
	}

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
	applyDone := make(chan struct{})
	go func() {
		select {
		case <-sigCh:
			fmt.Fprintln(os.Stderr, "\ninterrupted — detaching disk before exit...")
			close(interrupted)
		case <-applyDone:
		}
	}()
	defer func() {
		signal.Stop(sigCh)
		close(applyDone)
	}()

	select {
	case <-interrupted:
		return fmt.Errorf("interrupted")
	default:
	}

	// Build a shell script that enables ownership, copies files, sets modes
	// and ownership in one elevated pass. This avoids the problem where APFS
	// ownership is disabled (default for disk images) and we can't even
	// create files in system directories without root.
	if err := applyStagedFiles(target, stagingDir, mountPoint, dataPart, manifest); err != nil {
		return err
	}

	// Clean up staging directory on success.
	os.RemoveAll(stagingDir)
	markInjectSucceededForVM(target)
	if manifestIncludesAgent(manifest) {
		if err := setVMAgentRequested(target.Directory, detectVMAgentPlatform(target.Directory), true, vmAgentSourceProvision); err != nil {
			fmt.Printf("warning: save guest agent config: %v\n", err)
		}
	}

	fmt.Println("Provisioning applied.")
	return nil
}

func manifestIncludesAgent(manifest *ProvisionManifest) bool {
	if manifest == nil {
		return false
	}
	for _, file := range manifest.Files {
		switch filepath.Clean(file.Path) {
		case filepath.Join("usr", "local", "bin", agentBinaryName),
			filepath.Join("Library", "LaunchDaemons", agentLaunchDaemonLabel+".plist"),
			filepath.Join("Library", "LaunchAgents", agentLaunchAgentLabel+".plist"):
			return true
		}
	}
	return false
}

// applyStagedFiles enables APFS ownership, copies all staged files to the
// mount point, sets permissions and ownership. If not running as root, the
// entire operation runs via a native Security.framework authorization prompt.
func applyStagedFiles(target vmSelection, stagingDir, mountPoint, dataPart string, manifest *ProvisionManifest) error {
	// Pick a representative file we can post-verify chowned correctly. We
	// need at least one file with Owner=root:wheel to validate that the
	// owners-mount actually took effect.
	var verifyTarget string
	for _, f := range manifest.Files {
		if f.Owner == "root:wheel" {
			verifyTarget = filepath.Join(mountPoint, f.Path)
			break
		}
	}

	successMarker := tmpPathFor("vz-provision-apply-ok-")

	em := &elevatedManifest{
		RemountOwners:     []string{dataPart},
		VerifyChownTarget: verifyTarget,
		SuccessMarker:     successMarker,
	}
	for _, f := range manifest.Files {
		src := filepath.Join(stagingDir, f.Path)
		dst := filepath.Join(mountPoint, f.Path)
		em.MkdirAll = append(em.MkdirAll, filepath.Dir(dst))
		em.CopyFiles = append(em.CopyFiles, elevatedCopy{
			Src:   src,
			Dst:   dst,
			Mode:  f.Mode,
			Owner: f.Owner,
		})
	}

	if err := runElevated(em, elevationPrompt(
		fmt.Sprintf("Provision VM %q: write %d files (user account, agent, auto-login).", target.elevationLabel(), len(manifest.Files)),
	)); err != nil {
		return err
	}

	// Verify the privileged operation ran to completion. The success marker
	// is touched as the last manifest step, so its absence means we exited
	// before that point.
	if _, err := os.Stat(successMarker); err != nil {
		return fmt.Errorf("provisioning did not complete (missing success marker %s); check the error above for cause", successMarker)
	}
	os.Remove(successMarker)

	if verbose {
		for _, f := range manifest.Files {
			fmt.Printf("  applied: %s\n", f.Path)
		}
	}
	return nil
}

// tmpPathFor returns a unique temporary file path with the given prefix.
// The file is not created; this is just a path generator for use as a
// success-marker file checked by the caller after a privileged script runs.
func tmpPathFor(prefix string) string {
	f, err := os.CreateTemp("", prefix+"*")
	if err != nil {
		// Fall back to a deterministic-but-unique-enough path if temp dir
		// is wedged. Caller will see the "marker missing" error either way.
		return filepath.Join(os.TempDir(), fmt.Sprintf("%s%d", prefix, time.Now().UnixNano()))
	}
	path := f.Name()
	f.Close()
	os.Remove(path) // we just want the path; the script will create it
	return path
}

// elevationPrompt formats a single-sentence action description for the native
// authorization dialog. SecurityAgent prepends "cove wants to make changes"
// and the password field follows; the returned string appears between them.
// Keep the action under ~100 chars — SecurityAgent truncates longer strings.
func elevationPrompt(action string) string {
	const max = 140
	if len(action) > max {
		action = action[:max-1] + "…"
	}
	return action
}

// elevationVMLabel returns the VM name to show in auth dialogs, defaulting to
// "default" when no VM is selected yet.
func elevationVMLabel() string {
	return currentVMSelection().elevationLabel()
}

// errRestrictedNoElevation is returned when cove is invoked from a restricted
// environment (Claude Code, sandboxed shell) where the native authorization
// dialog cannot appear and no helper is available. Callers receiving this
// error should propagate it as-is; the relevant remediation has already been
// printed to stderr.
var errRestrictedNoElevation = errors.New("cannot prompt for elevation in restricted environment; run the printed command in a real terminal")

// restrictedEnvironment reports whether cove is running in a context that
// cannot show the native macOS authorization dialog. Detection is conservative:
// it returns true only when an unambiguous signal is present.
func restrictedEnvironment() bool {
	if os.Getenv("CLAUDECODE") == "1" {
		return true
	}
	if os.Getenv("IS_SANDBOX") == "1" {
		return true
	}
	if os.Getenv("COVE_FORCE_MANUAL_ELEVATION") == "1" {
		return true
	}
	return false
}

// stageAgent cross-compiles the vz-agent binary and stages it along with
// its LaunchDaemon plist. The build happens here (no root needed).
func stageAgent(stagingDir string, manifest *ProvisionManifest) error {
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
