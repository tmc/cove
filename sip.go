package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

// handleSIPCommand dispatches SIP management subcommands.
func handleSIPCommand(args []string) error {
	if len(args) == 0 {
		fmt.Println(sipUsage)
		return nil
	}

	switch args[0] {
	case "disable":
		return sipDisable()
	case "disable-auto":
		return sipDisableAuto(args[1:])
	case "enable":
		return sipEnable()
	case "status":
		return sipStatus()
	case "create-disk":
		return sipCreateDisk()
	case "help":
		fmt.Println(sipUsage)
		return nil
	default:
		return fmt.Errorf("unknown sip command: %s\n\n%s", args[0], sipUsage)
	}
}

func sipDisable() error {
	path, err := EnsureRecoveryDisk(vmDir)
	if err != nil {
		return fmt.Errorf("create recovery disk: %w", err)
	}

	fmt.Println()
	fmt.Println("To disable SIP:")
	fmt.Println()
	fmt.Println("  1. Boot into recovery with the tools disk attached:")
	fmt.Println("     vz-macos run -recovery -recovery-disk -gui")
	fmt.Println()
	fmt.Println("  2. In Recovery, open Terminal (Utilities > Terminal)")
	fmt.Println()
	fmt.Println("  3. Find and use the recovery disk:")
	fmt.Println("     diskutil list                    # Find VZRECOVERY volume")
	fmt.Println("     cd /Volumes/VZRECOVERY")
	fmt.Println("     sh csrutil-disable.sh")
	fmt.Println()
	fmt.Println("  4. Reboot:")
	fmt.Println("     reboot")
	fmt.Println()
	fmt.Printf("Recovery disk: %s\n", path)
	fmt.Println()
	fmt.Println("NOTE: csrutil disable requires a user with recovery authorization.")
	fmt.Println("The provision script runs 'diskutil apfs updatePreboot /' to register")
	fmt.Println("users for recovery. If that's not enough, use -bootstrap-recovery (default)")
	fmt.Println("which creates a two-user bootstrap for full recovery auth.")
	return nil
}

// sipDisableAuto attempts unattended SIP disable via recovery mode boot commands.
//
// This orchestrates:
//  1. Create recovery disk with csrutil scripts
//  2. Boot VM in recovery mode with recovery disk attached
//  3. Wait for recovery environment to load
//  4. Open Terminal (Utilities > Terminal via keyboard shortcut)
//  5. Type csrutil disable command and authenticate
//  6. Reboot
//
// The user must provide credentials for a recovery-authorized admin.
func sipDisableAuto(args []string) error {
	fs := flag.NewFlagSet("sip disable-auto", flag.ExitOnError)
	user := fs.String("user", "", "Admin username for recovery authentication (required)")
	pass := fs.String("password", "", "Admin password for recovery authentication (required)")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: vz-macos sip disable-auto [options]

Automated SIP disable via recovery mode.

This command boots the VM into recovery mode, opens Terminal, runs
'csrutil disable', authenticates with the provided credentials, and
reboots. The VM must already have a provisioned admin user with
recovery authorization (created via inject -bootstrap-recovery).

Options:
`)
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
Examples:
  # Disable SIP (user must have recovery authorization)
  vz-macos sip disable-auto -user testuser -password secret123

Prerequisites:
  - VM must be installed and provisioned (inject -bootstrap-recovery)
  - VM must NOT be currently running
  - User must have SecureToken and recovery authorization
`)
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *user == "" || *pass == "" {
		return fmt.Errorf("both -user and -password are required for automated SIP disable")
	}

	// Create recovery boot commands
	commands := generateSIPDisableBootCommands(*user, *pass)

	fmt.Println("=== Automated SIP Disable ===")
	fmt.Println()
	fmt.Printf("User: %s\n", *user)
	fmt.Printf("Boot commands: %d steps\n", len(commands))
	fmt.Println()
	fmt.Println("This will:")
	fmt.Println("  1. Create recovery tools disk")
	fmt.Println("  2. Boot VM in recovery mode with GUI")
	fmt.Println("  3. Open Terminal in recovery environment")
	fmt.Println("  4. Run csrutil disable")
	fmt.Println("  5. Authenticate as the provided user")
	fmt.Println("  6. Reboot the VM")
	fmt.Println()

	// Ensure recovery disk exists
	recoveryPath, err := EnsureRecoveryDisk(vmDir)
	if err != nil {
		return fmt.Errorf("create recovery disk: %w", err)
	}
	fmt.Printf("Recovery disk: %s\n", recoveryPath)

	// Write the boot commands to a temp file and print instructions
	// Since the actual VM boot requires the full event loop (GUI mode),
	// we output the boot commands file and instruct the user to use it.
	bootCmdsPath := writeBootCommandsForSIP(commands)

	fmt.Println()
	fmt.Println("=== Boot Commands Generated ===")
	fmt.Printf("Boot commands file: %s\n", bootCmdsPath)
	fmt.Println()
	fmt.Println("Run the following command to execute:")
	fmt.Println()
	fmt.Printf("  vz-macos run -recovery -recovery-disk -gui -boot-commands %s\n", bootCmdsPath)
	fmt.Println()
	fmt.Println("After the VM reboots, SIP should be disabled. Verify with:")
	fmt.Println("  vz-macos sip status")
	fmt.Println()
	fmt.Println("NOTE: If authentication fails, the user may not have recovery")
	fmt.Println("authorization. Ensure the user was created with -bootstrap-recovery.")

	return nil
}

// generateSIPDisableBootCommands creates boot commands for recovery mode SIP disable.
func generateSIPDisableBootCommands(username, password string) []BootCommand {
	return []BootCommand{
		// Wait for recovery environment to boot
		{Type: "wait", Args: "45s"},

		// Recovery mode shows a globe and then the macOS Recovery window.
		// We need to navigate to Utilities > Terminal.
		// In recovery, the menu bar has: Apple, Utilities, Window, Help
		// Keyboard shortcut: Cmd+Space doesn't work, but we can use the menu bar.

		// Wait for recovery to fully load - look for common recovery text
		{Type: "waitForText", Args: "Macintosh HD"},

		// Open Terminal via Utilities menu
		// Recovery menu: Utilities > Terminal
		// We can't use keyboard shortcuts directly but we can:
		// 1. Click on the Utilities menu (using text recognition)
		{Type: "click", Args: "Utilities"},
		{Type: "wait", Args: "1s"},
		{Type: "click", Args: "Terminal"},
		{Type: "wait", Args: "3s"},

		// Now we should have a Terminal window. Type the csrutil command.
		{Type: "type", Args: "csrutil disable"},
		{Type: "key", Args: "return"},
		{Type: "wait", Args: "2s"},

		// csrutil disable prompts for authentication:
		// "Enter your password to modify the security settings."
		// It shows a username prompt first, then password.
		{Type: "waitForText", Args: "password"},
		{Type: "wait", Args: "1s"},

		// Type the username
		{Type: "type", Args: username},
		{Type: "key", Args: "return"},
		{Type: "wait", Args: "1s"},

		// Type the password
		{Type: "type", Args: password},
		{Type: "key", Args: "return"},
		{Type: "wait", Args: "3s"},

		// Take a screenshot to see the result
		{Type: "screenshot", Args: ""},

		// Reboot
		{Type: "type", Args: "reboot"},
		{Type: "key", Args: "return"},
	}
}

// writeBootCommandsForSIP writes the boot commands to a file in the VM directory.
func writeBootCommandsForSIP(commands []BootCommand) string {
	path := fmt.Sprintf("%s/sip-disable-commands.txt", vmDir)

	var lines []string
	lines = append(lines, "# Auto-generated boot commands for SIP disable in recovery mode")
	lines = append(lines, "# Generated by: vz-macos sip disable-auto")
	lines = append(lines, "")

	for _, cmd := range commands {
		switch cmd.Type {
		case "wait":
			lines = append(lines, fmt.Sprintf("<wait %s>", cmd.Args))
		case "waitForText":
			lines = append(lines, fmt.Sprintf(`<waitForText "%s">`, cmd.Args))
		case "click":
			lines = append(lines, fmt.Sprintf(`<click "%s">`, cmd.Args))
		case "type":
			lines = append(lines, fmt.Sprintf(`<type "%s">`, cmd.Args))
		case "key":
			lines = append(lines, fmt.Sprintf("<key %s>", cmd.Args))
		case "screenshot":
			lines = append(lines, "<screenshot>")
		}
	}

	content := strings.Join(lines, "\n") + "\n"
	os.WriteFile(path, []byte(content), 0644)
	return path
}

func sipEnable() error {
	path, err := EnsureRecoveryDisk(vmDir)
	if err != nil {
		return fmt.Errorf("create recovery disk: %w", err)
	}

	fmt.Println()
	fmt.Println("To enable SIP:")
	fmt.Println()
	fmt.Println("  1. Boot into recovery with the tools disk attached:")
	fmt.Println("     vz-macos run -recovery -recovery-disk -gui")
	fmt.Println()
	fmt.Println("  2. In Recovery, open Terminal (Utilities > Terminal)")
	fmt.Println()
	fmt.Println("  3. Find and use the recovery disk:")
	fmt.Println("     diskutil list                    # Find VZRECOVERY volume")
	fmt.Println("     cd /Volumes/VZRECOVERY")
	fmt.Println("     sh csrutil-enable.sh")
	fmt.Println()
	fmt.Println("  4. Reboot:")
	fmt.Println("     reboot")
	fmt.Println()
	fmt.Printf("Recovery disk: %s\n", path)
	return nil
}

func sipStatus() error {
	// Try to check via agent first (if VM is running)
	sock := GetControlSocketPath()
	resp, err := ctlSendCommand(sock, "agent-exec", map[string]interface{}{
		"args": []string{"csrutil", "status"},
	}, 5*time.Second)
	if err == nil && resp.Success {
		output := parseAgentExecOutput(resp.Data)
		fmt.Printf("SIP status (from running VM): %s\n", output)
		return nil
	}

	// Fallback to instructions
	fmt.Println("SIP status can only be checked from within the VM.")
	fmt.Println()
	fmt.Println("If the VM is running with the agent:")
	fmt.Println("  vz-macos ctl agent-exec csrutil status")
	fmt.Println()
	fmt.Println("From a running VM (Terminal.app):")
	fmt.Println("  csrutil status")
	fmt.Println()
	fmt.Println("From Recovery Mode (Utilities > Terminal):")
	fmt.Println("  csrutil status")
	return nil
}

func sipCreateDisk() error {
	path := RecoveryDiskPath(vmDir)

	// Force re-creation by removing existing disk
	if _, err := os.Stat(path); err == nil {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove existing recovery disk: %w", err)
		}
	}

	if err := CreateRecoveryDisk(path); err != nil {
		return fmt.Errorf("create recovery disk: %w", err)
	}
	return nil
}

const sipUsage = `Usage: vz-macos sip <command>

Commands:
  disable        Create recovery disk and show instructions to disable SIP
  disable-auto   Generate boot commands for automated SIP disable in recovery
  enable         Create recovery disk and show instructions to enable SIP
  status         Check SIP status (queries agent if VM is running)
  create-disk    Create (or recreate) the recovery tools disk

The recovery disk is a small FAT32 image containing helper scripts for
csrutil operations. It is automatically created when needed and attached
as a USB storage device in recovery mode.

Automated SIP Disable:
  The 'disable-auto' command generates boot commands that automate the
  recovery mode workflow. It requires a user with recovery authorization:

    vz-macos sip disable-auto -user testuser -password secret123

  This generates a boot commands file and prints the run command to
  execute it. The automation opens Terminal in recovery, runs csrutil
  disable, authenticates, and reboots.

Recovery Authorization:
  csrutil requires an admin user with recovery authorization. The
  provision script automatically runs 'diskutil apfs updatePreboot /'
  to register users for LocalPolicy/recovery operations.

  By default, inject uses -bootstrap-recovery which creates a hidden
  bootstrap admin first, then creates your user via that admin. Users
  created BY a SecureToken-bearing admin get full recovery auth.

  If recovery still won't authorize your user:
    1. Create a second admin inside the VM:
       sysadminctl -addUser recovery-admin -password ... -admin
    2. Use that admin to authorize csrutil in recovery mode`
