package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// handleSIPCommand dispatches SIP management subcommands.
func handleSIPCommand(args []string) error {
	if len(args) == 0 {
		fmt.Print(sipUsage)
		return nil
	}

	switch args[0] {
	case "enable":
		return sipEnable()
	case "enable-auto":
		return sipAuto("enable", args[1:])
	case "disable":
		return sipDisable()
	case "disable-auto":
		return sipAuto("disable", args[1:])
	case "status":
		return sipStatus()
	case "create-disk":
		return sipCreateDisk()
	case "help":
		fmt.Print(sipUsage)
		return nil
	default:
		return fmt.Errorf("unknown sip command: %s\n\n%s", args[0], sipUsage)
	}
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
	fmt.Printf("     vz-macos -vm %s run -recovery -gui -usb %q\n", sipTargetVMName(), path)
	fmt.Println()
	fmt.Println("  2. In Recovery, open Terminal (Utilities > Terminal)")
	fmt.Println()
	fmt.Println("  3. Run:")
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

func sipDisable() error {
	path, err := EnsureRecoveryDisk(vmDir)
	if err != nil {
		return fmt.Errorf("create recovery disk: %w", err)
	}

	fmt.Println()
	fmt.Println("To disable SIP:")
	fmt.Println()
	fmt.Println("  1. Boot into recovery with the tools disk attached:")
	fmt.Printf("     vz-macos -vm %s run -recovery -gui -usb %q\n", sipTargetVMName(), path)
	fmt.Println()
	fmt.Println("  2. In Recovery, open Terminal (Utilities > Terminal)")
	fmt.Println()
	fmt.Println("  3. Run:")
	fmt.Println("     diskutil list                    # Find VZRECOVERY volume")
	fmt.Println("     cd /Volumes/VZRECOVERY")
	fmt.Println("     sh csrutil-disable.sh")
	fmt.Println()
	fmt.Println("  4. Reboot:")
	fmt.Println("     reboot")
	fmt.Println()
	fmt.Printf("Recovery disk: %s\n", path)
	return nil
}

// sipAuto generates automation files for recovery-mode csrutil changes.
func sipAuto(mode string, args []string) error {
	fs := flag.NewFlagSet("sip "+mode+"-auto", flag.ExitOnError)
	user := fs.String("user", "", "admin username for recovery authentication (optional)")
	pass := fs.String("password", "", "admin password for recovery authentication (optional)")
	confirm := fs.Bool("confirm", false, "answer y after authentication (only for builds that prompt confirmation)")
	noReboot := fs.Bool("no-reboot", false, "do not append reboot command (for iterative debugging)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	recoveryPath, err := EnsureRecoveryDisk(vmDir)
	if err != nil {
		return fmt.Errorf("create recovery disk: %w", err)
	}

	commands := generateSIPBootCommands(mode, *user, *pass, *confirm, !*noReboot)
	bootCmdsPath, err := writeBootCommandsForSIP(vmDir, mode, commands)
	if err != nil {
		return fmt.Errorf("write boot commands: %w", err)
	}

	fmt.Printf("Generated %d SIP %s boot commands.\n", len(commands), mode)
	fmt.Printf("Recovery disk: %s\n", recoveryPath)
	fmt.Printf("Boot commands: %s\n", bootCmdsPath)
	if *user == "" && *pass == "" {
		fmt.Println("Auth mode: no explicit credentials (script relies on csrutil not prompting)")
	} else if *user == "" {
		fmt.Println("Auth mode: password-only")
	} else {
		fmt.Printf("Auth mode: username %q provided\n", *user)
	}
	fmt.Println()
	fmt.Println("Run this to execute automation:")
	fmt.Println()
	fmt.Printf("  vz-macos -vm %s run -recovery -no-resume -gui -unattended -usb %q -boot-commands %q\n",
		sipTargetVMName(), recoveryPath, bootCmdsPath)
	fmt.Println()
	fmt.Printf("After reboot, verify with:\n  vz-macos -vm %s sip status\n", sipTargetVMName())
	return nil
}

func sipStatus() error {
	client := NewControlClient(GetControlSocketPath())
	client.SetTimeout(10 * time.Second)
	resp, err := client.AgentExecTyped([]string{"csrutil", "status"}, nil, "")
	if err == nil {
		out := strings.TrimSpace(resp.GetStdout())
		if out == "" {
			out = strings.TrimSpace(resp.GetStderr())
		}
		if out == "" {
			out = "no output"
		}
		fmt.Printf("SIP status (from running VM): %s\n", out)
		return nil
	}

	fmt.Println("SIP status can only be queried from a running VM with guest agent.")
	fmt.Println()
	fmt.Println("Options:")
	fmt.Printf("  vz-macos -vm %s ctl -wait 60s agent-exec csrutil status\n", sipTargetVMName())
	fmt.Println("  (or from guest/recovery terminal) csrutil status")
	return nil
}

func sipCreateDisk() error {
	path := RecoveryDiskPath(vmDir)
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

func generateSIPBootCommands(mode, username, password string, confirm, reboot bool) []BootCommand {
	csr := "csrutil " + mode
	cmds := []BootCommand{
		{Type: "wait", Args: "45s"},
		// Startup Options screen.
		{Type: "waitForText", Args: "Options"},
		{Type: "click", Args: "Options"},
		{Type: "wait", Args: "1s"},
		{Type: "waitForText", Args: "Continue"},
		{Type: "click", Args: "Continue"},
		{Type: "wait", Args: "20s"},
		// Open Recovery Terminal from menu bar for deterministic routing.
		{Type: "waitForMenuText", Args: "Utilities"},
		{Type: "clickMenuItem", Args: "Utilities|Terminal"},
		{Type: "wait", Args: "3s"},
		{Type: "type", Args: csr},
		{Type: "key", Args: "return"},
		{Type: "wait", Args: "2s"},
	}

	if username != "" {
		cmds = append(cmds,
			BootCommand{Type: "typeAndReturnIfText", Args: "Enter username|" + username},
			BootCommand{Type: "typeAndReturnIfText", Args: "user name|" + username},
			BootCommand{Type: "wait", Args: "1s"},
		)
	}
	if password != "" {
		cmds = append(cmds,
			BootCommand{Type: "typeAndReturnIfText", Args: "Enter password|" + password},
			BootCommand{Type: "wait", Args: "3s"},
		)
	}
	if confirm {
		cmds = append(cmds,
			BootCommand{Type: "typeAndReturnIfText", Args: "Are you sure|y"},
			BootCommand{Type: "wait", Args: "1s"},
		)
	}

	if username != "" || password != "" {
		cmds = append(cmds,
			BootCommand{Type: "type", Args: "csrutil status"},
			BootCommand{Type: "key", Args: "return"},
			BootCommand{Type: "wait", Args: "2s"},
		)
	}

	cmds = append(cmds, BootCommand{Type: "screenshot", Args: ""})
	if reboot {
		cmds = append(cmds,
			BootCommand{Type: "type", Args: "reboot"},
			BootCommand{Type: "key", Args: "return"},
		)
	}
	return cmds
}

func writeBootCommandsForSIP(vmDirectory, mode string, commands []BootCommand) (string, error) {
	base := fmt.Sprintf("sip-%s-commands.txt", mode)
	path := filepath.Join(vmDirectory, base)

	lines := []string{
		fmt.Sprintf("# Auto-generated boot commands for SIP %s in recovery mode", mode),
		fmt.Sprintf("# Generated by: vz-macos sip %s-auto", mode),
		"",
	}
	for _, cmd := range commands {
		switch cmd.Type {
		case "wait":
			lines = append(lines, fmt.Sprintf("<wait %s>", cmd.Args))
		case "waitForText":
			lines = append(lines, fmt.Sprintf(`<waitForText "%s">`, cmd.Args))
		case "waitForMenuText":
			lines = append(lines, fmt.Sprintf(`<waitForMenuText "%s">`, cmd.Args))
		case "click":
			lines = append(lines, fmt.Sprintf(`<click "%s">`, cmd.Args))
		case "clickMenu":
			lines = append(lines, fmt.Sprintf(`<clickMenu "%s">`, cmd.Args))
		case "clickMenuItem":
			lines = append(lines, fmt.Sprintf(`<clickMenuItem "%s">`, cmd.Args))
		case "type":
			lines = append(lines, fmt.Sprintf(`<type "%s">`, cmd.Args))
		case "typeAndReturnIfText":
			lines = append(lines, fmt.Sprintf(`<typeAndReturnIfText "%s">`, cmd.Args))
		case "key":
			lines = append(lines, fmt.Sprintf("<key %s>", cmd.Args))
		case "screenshot":
			lines = append(lines, "<screenshot>")
		}
	}
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "", err
	}
	return path, nil
}

func sipTargetVMName() string {
	if vmName != "" {
		return vmName
	}
	if vmDir != "" {
		return filepath.Base(vmDir)
	}
	return "default"
}

const sipUsage = `Usage: vz-macos sip <command>

Commands:
  enable         Create recovery disk and show instructions to enable SIP
  enable-auto    Generate recovery boot commands for automated SIP enable
  disable        Create recovery disk and show instructions to disable SIP
  disable-auto   Generate recovery boot commands for automated SIP disable
  status         Check SIP status (queries running guest agent)
  create-disk    Create (or recreate) the recovery tools disk

Examples:
  vz-macos -vm macos-3 sip enable-auto -user user -password secret
  vz-macos -vm macos-3 sip disable-auto -password secret -confirm
  vz-macos -vm macos-3 sip enable-auto -no-reboot
  vz-macos -vm macos-3 run -recovery -no-resume -gui -unattended -usb ~/.vz/vms/macos-3/recovery-disk.img -boot-commands ~/.vz/vms/macos-3/sip-enable-commands.txt
`
