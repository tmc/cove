package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/term"
)

// ErrInjectFlagRequired is returned by the inject CLI when a required
// flag (-user, -password) is missing and cannot be filled from a
// terminal prompt. Callers can branch on this with errors.Is to
// surface a usage hint without parsing the message.
var ErrInjectFlagRequired = errors.New("inject required flag missing")

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
//	./cove provision -user testuser -skip-setup-assistant
//
// For CI/headless use, the phases can be run separately:
//
//	./cove provision -user testuser -stage-only
//	sudo ./cove provision --apply
func handleProvision(args []string) error {
	// Check environment variable for debug mode
	provisionVerbose = os.Getenv("VZ_DEBUG_INJECT") == "1"
	target := currentVMSelection()

	fs, user, password, admin, skipSetup, autoLogin, noAutoLogin, createUserPlist, uid, sshKeyPath, installXcodeCLI, verboseFlag, applyOnly, stageOnly, force, enableAgent, enableGuestTools, enableSSHD, bootstrapRecovery, noBootstrapRecovery := newInjectFlagSet()

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	// Enable verbose mode from flag
	if *verboseFlag {
		provisionVerbose = true
	}

	// Apply-only mode: read manifest and apply staged files to disk.
	if *applyOnly {
		return applyProvisioningFilesForVMForce(target, *force)
	}

	if !*stageOnly && !*force && didInjectSucceedForVM(target) {
		printProvisioningAlreadyApplied(target)
		return nil
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
		return provisionAgent()
	}

	if *user == "" {
		return fmt.Errorf("%w: -user", ErrInjectFlagRequired)
	}
	if *password == "" {
		pw, err := readPassword("Password: ")
		if err != nil {
			return fmt.Errorf("read password: %w", err)
		}
		*password = string(pw)
		if *password == "" {
			return fmt.Errorf("%w: -password", ErrInjectFlagRequired)
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
	provisionLog("  EnableGuestTools: %v", *enableGuestTools)
	provisionLog("  StageOnly: %v", *stageOnly)
	provisionLog("  VM Dir: %s", target.Directory)

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

	opts := InjectOptions{
		Config:             config,
		SkipSetupAssistant: *skipSetup,
		AutoLogin:          effectiveAutoLogin,
		CreateUserPlist:    *createUserPlist,
		UID:                *uid,
		SSHKeyPath:         *sshKeyPath,
		InjectAgent:        *enableAgent,
		InjectGuestTools:   *enableGuestTools,
		BootstrapRecovery:  *bootstrapRecovery,
		EnableSSHD:         *enableSSHD,
		Force:              *force,
	}

	// Phase 1: Stage all files (builds, downloads, generates — no root needed).
	if _, err := stageProvisioningFilesForVM(target, opts); err != nil {
		return err
	}

	if *stageOnly {
		fmt.Println()
		fmt.Println("Files staged successfully. To apply to the VM disk, run:")
		vmFlag := target.hintFlag()
		fmt.Printf("  sudo ./cove%s provision -apply\n", vmFlag)
		fmt.Println()
		fmt.Println("Or without sudo (will show a native macOS authorization prompt):")
		fmt.Printf("  ./cove%s provision -apply\n", vmFlag)
		return nil
	}

	// Phase 2: Apply staged files to the VM disk.
	return applyProvisioningFilesForVMForce(target, *force)
}

func newInjectFlagSet() (*flag.FlagSet, *string, *string, *bool, *bool, *bool, *bool, *bool, *int, *string, *bool, *bool, *bool, *bool, *bool, *bool, *bool, *bool, *bool, *bool) {
	fs := flag.NewFlagSet("provision", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	user := fs.String("user", "", "Username for the provisioned user (required)")
	password := fs.String("password", "", "Password for the provisioned user (prompts if empty)")
	admin := fs.Bool("admin", true, "Make the user an admin")
	skipSetup := fs.Bool("skip-setup-assistant", false, "Skip Setup Assistant by creating .AppleSetupDone")
	autoLogin := fs.Bool("auto-login", true, "Enable automatic login for the user")
	noAutoLogin := fs.Bool("no-auto-login", false, "Disable automatic login")
	createUserPlist := fs.Bool("plist", false, "Create the user plist directly (advanced: bypasses sysadminctl)")
	uid := fs.Int("uid", 501, "User ID for plist mode")
	sshKeyPath := fs.String("ssh-key", "", "Path to SSH public key file for authorized_keys")
	installXcodeCLI := fs.Bool("xcode-cli", false, "Install Xcode Command Line Tools during provisioning")
	verboseFlag := fs.Bool("v", false, "Verbose output (or set VZ_DEBUG_INJECT=1)")
	applyOnly := fs.Bool("apply", false, "Apply previously staged provisioning files to the VM disk")
	stageOnly := fs.Bool("stage-only", false, "Stage files only (no disk mount); prints the apply command")
	force := fs.Bool("force", false, "Re-stage and re-apply even if provisioning already succeeded")
	enableAgent := fs.Bool("agent", true, "Cross-compile and inject the vz-agent gRPC daemon")
	enableGuestTools := fs.Bool("guest-tools", true, "Download and inject SPICE guest tools for clipboard sharing")
	enableSSHD := fs.Bool("enable-sshd", false, "Enable SSH daemon (Remote Login) on first boot")
	bootstrapRecovery := fs.Bool("bootstrap-recovery", true, "Create a hidden admin first to grant full recovery authorization")
	noBootstrapRecovery := fs.Bool("no-bootstrap-recovery", false, "Disable the two-user recovery bootstrap")
	fs.Usage = func() {
		printInjectUsage(os.Stderr, fs)
	}
	return fs, user, password, admin, skipSetup, autoLogin, noAutoLogin, createUserPlist, uid, sshKeyPath, installXcodeCLI, verboseFlag, applyOnly, stageOnly, force, enableAgent, enableGuestTools, enableSSHD, bootstrapRecovery, noBootstrapRecovery
}

func printInjectUsage(w io.Writer, fs *flag.FlagSet) {
	fmt.Fprintf(w, `Usage: cove provision [options]

Provision a VM with a user account, auto-login, and guest tools.

Two-phase provisioning:
  Phase 1 (no admin): build binaries, download tools, generate scripts
  Phase 2 (may need admin): mount the VM disk, copy files, fix ownership

By default both phases run in one command. Use -stage-only and -apply
to split them for CI or to avoid building as admin.

Provisioning modes:
  1. LaunchDaemon mode (default): run sysadminctl on first boot
  2. Plist mode (-plist): create the user plist directly (advanced)

Options:
`)
	fs.PrintDefaults()
	fmt.Fprintf(w, `
Examples:
  cove provision -user testuser -skip-setup-assistant
  cove provision -user testuser -stage-only
  cove provision -apply
  cove provision -user testuser -ssh-key ~/.ssh/id_rsa.pub

Recovery authorization:
  By default, provision uses a two-user bootstrap. A hidden admin is created
  first, then your user is created by that admin so the account has full
  recovery authorization. Use -no-bootstrap-recovery to disable that flow.
`)
}

// readPassword prompts for a password, trying multiple approaches:
//  1. osascript GUI dialog (first when GUI mode is active)
//  2. /dev/tty with term.ReadPassword
//  3. os.Stdin plain read (last resort, password will echo)
func readPassword(prompt string) ([]byte, error) {
	if preferPasswordDialog {
		pw, err := readPasswordViaDialog(prompt)
		if err == nil && len(pw) > 0 {
			return pw, nil
		}
		if err != nil && strings.Contains(err.Error(), "canceled") {
			return nil, err
		}
	}

	// Try /dev/tty.
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
	}

	// Try osascript GUI dialog as fallback when terminal input is unavailable.
	if !preferPasswordDialog {
		pw, err := readPasswordViaDialog(prompt)
		if err == nil && len(pw) > 0 {
			return pw, nil
		}
		if err != nil && strings.Contains(err.Error(), "canceled") {
			return nil, err
		}
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
		`display dialog %q default answer "" with hidden answer buttons {"Cancel","OK"} default button "OK" with title "cove"`,
		prompt,
	)
	cmd := exec.Command("osascript", "-e", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "User canceled") || strings.Contains(msg, "(-128)") {
			return nil, fmt.Errorf("canceled")
		}
		if msg != "" {
			return nil, fmt.Errorf("osascript: %s", msg)
		}
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
