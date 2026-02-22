package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/term"
)

// InjectOptions configures the inject behavior
type InjectOptions struct {
	Config             ProvisionConfig
	SkipSetupAssistant bool
	AutoLogin          bool
	CreateUserPlist    bool // Create user plist directly instead of using LaunchDaemon
	UID                int
	SSHKeyPath         string              // Path to SSH public key file for authorized_keys
	UserDataConfig     *UserDataConfig     // Optional user data disk configuration
	ScriptsConfig      *ScriptsShareConfig // Optional scripts runner configuration
	InjectAgent        bool                // Cross-compile and inject the vz-agent GRPC daemon
	InjectGuestTools   bool                // Download and inject SPICE guest tools for clipboard sharing
	BootstrapRecovery  bool                // Two-user bootstrap: create hidden admin first, then real user
	EnableSSHD         bool                // Enable SSH daemon (Remote Login) on first boot
}

// provisionVerbose controls verbose output for provision operations.
var provisionVerbose bool

// provisionLog prints a message if verbose mode is enabled.
func provisionLog(format string, args ...interface{}) {
	if provisionVerbose {
		fmt.Printf("[provision] "+format+"\n", args...)
	}
}

// handleProvision handles the provision subcommand.
//
// Two-phase provisioning separates expensive operations (go build, curl downloads)
// from disk operations that need elevated privileges. By default, both phases run
// in a single invocation:
//
//	./vz-macos provision -user testuser -password secret123 -skip-setup-assistant
//
// For CI/headless use, the phases can be run separately:
//
//	./vz-macos provision -user testuser -password secret123 -stage-only
//	sudo ./vz-macos provision --apply
func handleProvision(args []string) error {
	// Check environment variable for debug mode
	provisionVerbose = os.Getenv("VZ_DEBUG_INJECT") == "1"

	// Parse flags specific to inject command
	fs := flag.NewFlagSet("provision", flag.ExitOnError)
	user := fs.String("user", "", "Username for the provisioned user (required)")
	password := fs.String("password", "", "Password for the provisioned user (required)")
	admin := fs.Bool("admin", true, "Make the user an admin")
	skipSetup := fs.Bool("skip-setup-assistant", false, "Skip Setup Assistant by creating .AppleSetupDone")
	autoLogin := fs.Bool("auto-login", true, "Enable automatic login for the user (default: true)")
	noAutoLogin := fs.Bool("no-auto-login", false, "Disable automatic login")
	createUserPlist := fs.Bool("plist", false, "Create user plist directly (advanced: bypasses sysadminctl)")
	uid := fs.Int("uid", 501, "User ID for plist mode")
	sshKeyPath := fs.String("ssh-key", "", "Path to SSH public key file for authorized_keys")
	installXcodeCLI := fs.Bool("xcode-cli", false, "Install Xcode Command Line Tools during provisioning")
	verboseFlag := fs.Bool("v", false, "Verbose output (or set VZ_DEBUG_INJECT=1)")

	// Two-phase provisioning flags
	applyOnly := fs.Bool("apply", false, "Apply previously staged provisioning files to VM disk (requires staged files)")
	stageOnly := fs.Bool("stage-only", false, "Stage files only (no disk mount); prints the apply command")

	// User data disk options
	enableUserData := fs.Bool("userdata", false, "Create and configure separate user data disk")
	userDataPath := fs.String("userdata-path", "", "Path for user data disk (default: vmDir/userdata.sparsebundle)")
	userDataSize := fs.Uint64("userdata-size", 32, "Size of user data disk in GB")
	userDataStrategy := fs.String("userdata-strategy", "volumes", "Mount strategy: volumes, symlinks, direct")
	userDataEphemeral := fs.Bool("userdata-ephemeral", false, "Mark as ephemeral (CI/CD mode, discard changes)")

	// Scripts runner options
	enableScriptsRunner := fs.Bool("scripts", false, "Inject scripts runner LaunchDaemon for VirtioFS scripts share")
	scriptsRunOnBoot := fs.Bool("scripts-run", true, "Run bootstrap script on boot (default: true)")

	// Guest agent options
	enableAgent := fs.Bool("agent", true, "Cross-compile and inject vz-agent GRPC daemon (default: true)")

	// Guest tools (SPICE clipboard) options
	enableGuestTools := fs.Bool("guest-tools", true, "Download and inject SPICE guest tools for clipboard sharing (default: true)")

	// SSH daemon (Remote Login)
	enableSSHD := fs.Bool("enable-sshd", false, "Enable SSH daemon (Remote Login) on first boot")

	// Recovery bootstrap options
	bootstrapRecovery := fs.Bool("bootstrap-recovery", true, "Two-user bootstrap: create hidden admin first to grant full recovery auth (default: true)")
	noBootstrapRecovery := fs.Bool("no-bootstrap-recovery", false, "Disable two-user bootstrap (single user, may lack recovery auth)")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: vz-macos provision [options]

Provision a VM with user account, auto-login, and guest tools.

Two-phase provisioning:
  Phase 1 (no root): Builds agent binary, downloads guest tools, generates scripts.
  Phase 2 (may need admin): Mounts VM disk, copies files, sets ownership.

By default both phases run in a single command. Use -stage-only and -apply
to run them separately (useful for CI or to avoid building as root).

Provisioning modes:
  1. LaunchDaemon mode (default): Writes a script that runs sysadminctl on first boot
  2. Plist mode (-plist): Directly creates the user plist with password hash (advanced)

Options:
`)
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
Examples:
  # Standard provisioning (builds, downloads, then writes to disk)
  vz-macos provision -user testuser -skip-setup-assistant

  # Two-phase: stage first (no root), then apply
  vz-macos provision -user testuser -password secret123 -stage-only
  sudo vz-macos provision -apply

  # Apply previously staged files
  vz-macos provision -apply

  # With separate user data disk (golden image workflow)
  vz-macos provision -user testuser -password secret123 -skip-setup-assistant -userdata

  # With SSH key for remote access
  vz-macos provision -user testuser -password secret123 -skip-setup-assistant -ssh-key ~/.ssh/id_rsa.pub

Recovery Authorization:
  By default, provision uses a two-user bootstrap (-bootstrap-recovery=true):
  a hidden admin is created first, then your user is created BY that admin,
  granting full recovery auth. Use -no-bootstrap-recovery to disable.
`)
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	// Enable verbose mode from flag
	if *verboseFlag {
		provisionVerbose = true
	}

	// Apply-only mode: read manifest and apply staged files to disk.
	if *applyOnly {
		return applyProvisioningFiles()
	}

	// Agent-only mode: if -agent was explicitly passed but no -user, just
	// inject the vz-agent binary and LaunchDaemon.
	agentExplicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "agent" {
			agentExplicit = true
		}
	})
	if *user == "" && agentExplicit && *enableAgent {
		return injectAgentOnly()
	}

	if *user == "" {
		return fmt.Errorf("missing required flag: -user")
	}
	if *password == "" {
		pw, err := readPassword("Password: ")
		if err != nil {
			return fmt.Errorf("read password: %w", err)
		}
		*password = string(pw)
		if *password == "" {
			return fmt.Errorf("missing required flag: -password")
		}
	}

	// Determine effective auto-login setting
	// Default is true, but can be disabled with -no-auto-login
	effectiveAutoLogin := *autoLogin && !*noAutoLogin

	provisionLog("Starting provision with options:")
	provisionLog("  Username: %s", *user)
	provisionLog("  Admin: %v", *admin)
	provisionLog("  SkipSetupAssistant: %v", *skipSetup)
	provisionLog("  AutoLogin: %v (no-auto-login=%v)", effectiveAutoLogin, *noAutoLogin)
	provisionLog("  CreateUserPlist: %v", *createUserPlist)
	provisionLog("  UID: %d", *uid)
	provisionLog("  SSHKeyPath: %s", *sshKeyPath)
	provisionLog("  EnableUserData: %v", *enableUserData)
	provisionLog("  EnableGuestTools: %v", *enableGuestTools)
	provisionLog("  StageOnly: %v", *stageOnly)
	provisionLog("  VM Dir: %s", vmDir)

	effectiveBootstrap := *bootstrapRecovery && !*noBootstrapRecovery
	config := ProvisionConfig{
		Username:          *user,
		Password:          *password,
		Fullname:          *user,
		Admin:             *admin,
		BootstrapRecovery: effectiveBootstrap,
		InstallXcodeCLI:   *installXcodeCLI,
		EnableSSHD:        *enableSSHD,
	}

	// Build user data config if enabled
	var userDataConfig *UserDataConfig
	if *enableUserData {
		strategy, err := ParseMountStrategy(*userDataStrategy)
		if err != nil {
			return fmt.Errorf("invalid mount strategy: %w", err)
		}

		path := *userDataPath
		if path == "" {
			path = DefaultUserDataPath(vmDir)
		}

		userDataConfig = &UserDataConfig{
			Enabled:       true,
			Path:          path,
			SizeGB:        *userDataSize,
			MountStrategy: strategy,
			Ephemeral:     *userDataEphemeral,
		}
	}

	// Build scripts config if enabled
	var scriptsConfig *ScriptsShareConfig
	if *enableScriptsRunner {
		scriptsConfig = &ScriptsShareConfig{
			Enabled:   true,
			HostPath:  DefaultScriptsPath(vmDir),
			RunOnBoot: *scriptsRunOnBoot,
		}
	}

	opts := InjectOptions{
		Config:             config,
		SkipSetupAssistant: *skipSetup,
		AutoLogin:          effectiveAutoLogin,
		CreateUserPlist:    *createUserPlist,
		UID:                *uid,
		SSHKeyPath:         *sshKeyPath,
		UserDataConfig:     userDataConfig,
		ScriptsConfig:      scriptsConfig,
		InjectAgent:        *enableAgent,
		InjectGuestTools:   *enableGuestTools,
		BootstrapRecovery:  *bootstrapRecovery,
		EnableSSHD:         *enableSSHD,
	}

	// Phase 1: Stage all files (builds, downloads, generates — no root needed).
	if _, err := stageProvisioningFiles(opts); err != nil {
		return err
	}

	if *stageOnly {
		fmt.Println()
		fmt.Println("Files staged successfully. To apply to the VM disk, run:")
		vmFlag := ""
		if vmName != "" {
			vmFlag = fmt.Sprintf(" -vm %s", vmName)
		}
		fmt.Printf("  sudo ./vz-macos%s provision -apply\n", vmFlag)
		fmt.Println()
		fmt.Println("Or without sudo (will prompt for admin password via GUI dialog):")
		fmt.Printf("  ./vz-macos%s provision -apply\n", vmFlag)
		return nil
	}

	// Phase 2: Apply staged files to the VM disk.
	return applyProvisioningFiles()
}

// readPassword prompts for a password, trying multiple approaches:
//  1. /dev/tty with term.ReadPassword (best: secure, works in normal terminals)
//  2. osascript GUI dialog (works when no tty, e.g., macgo re-exec)
//  3. os.Stdin plain read (last resort, password will echo)
func readPassword(prompt string) ([]byte, error) {
	// Try /dev/tty first — works in normal terminal sessions.
	if tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
		defer tty.Close()
		fmt.Fprint(tty, prompt)
		pw, err := term.ReadPassword(int(tty.Fd()))
		fmt.Fprintln(tty)
		if err == nil {
			if len(pw) == 0 {
				return nil, fmt.Errorf("interrupted (disk will be detached)")
			}
			return pw, nil
		}
		// Fall through to other methods.
	}

	// Try osascript GUI dialog — works in macgo re-exec where /dev/tty
	// is unavailable. Shows a native macOS password dialog.
	pw, err := readPasswordViaDialog(prompt)
	if err == nil && len(pw) > 0 {
		return pw, nil
	}

	// Last resort: read from os.Stdin (macgo pipe). Password will echo
	// since we can't control the parent terminal's echo state.
	fmt.Fprintf(os.Stderr, "%s(warning: input may echo) ", prompt)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		line := scanner.Text()
		if len(line) == 0 {
			return nil, fmt.Errorf("interrupted (disk will be detached)")
		}
		fmt.Fprintln(os.Stderr)
		return []byte(line), nil
	}
	return nil, fmt.Errorf("interrupted (disk will be detached)")
}

// readPasswordViaDialog uses osascript to show a native macOS password dialog.
func readPasswordViaDialog(prompt string) ([]byte, error) {
	script := fmt.Sprintf(
		`display dialog %q default answer "" with hidden answer buttons {"Cancel","OK"} default button "OK" with title "vz-macos"`,
		prompt,
	)
	cmd := exec.Command("osascript", "-e", script)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	// Output format: "button returned:OK, text returned:thepassword\n"
	s := strings.TrimSpace(string(out))
	if idx := strings.Index(s, "text returned:"); idx >= 0 {
		return []byte(s[idx+len("text returned:"):]), nil
	}
	return nil, fmt.Errorf("unexpected osascript output")
}

// readLine prompts for a line of input, trying /dev/tty then os.Stdin.
func readLine(prompt string) (string, error) {
	// Try /dev/tty first.
	if tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
		defer tty.Close()
		fmt.Fprint(tty, prompt)
		scanner := bufio.NewScanner(tty)
		if scanner.Scan() {
			return scanner.Text(), nil
		}
		return "", fmt.Errorf("no input")
	}
	// Fall back to os.Stdin (macgo pipe).
	fmt.Print(prompt)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		return scanner.Text(), nil
	}
	return "", fmt.Errorf("no input")
}
