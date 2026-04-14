package main

import (
	"flag"
	"fmt"
	"net/url"
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

	scriptData := generateSIPVZScript(mode, *user, *pass, *confirm, !*noReboot)
	bootCmdsPath, err := writeVZScriptForSIP(vmDir, mode, scriptData)
	if err != nil {
		return fmt.Errorf("write sip automation script: %w", err)
	}

	fmt.Printf("Generated SIP %s vzscript automation.\n", mode)
	fmt.Printf("Recovery disk: %s\n", recoveryPath)
	fmt.Printf("Automation script: %s\n", bootCmdsPath)
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

func generateSIPVZScript(mode, username, password string, confirm, reboot bool) string {
	var lines []string
	lines = append(lines,
		fmt.Sprintf("# Auto-generated vzscript for SIP %s in recovery mode", mode),
		fmt.Sprintf("# Generated by: vz-macos sip %s-auto", mode),
		"",
		"wait 45s",
		"ocr-wait "+scriptQuote("Options")+" 60s",
		"startup-options",
		"wait 20s",
		"recovery-continue",
		"key cmd+shift+t",
		"ocr-wait "+scriptQuote("-bash-3.2#")+" 20s",
		"type "+scriptQuote("csrutil "+mode),
		"key return",
		"wait 2s",
	)

	if confirm {
		lines = append(lines, sipConfirmVZScriptLines()...)
	}
	if username != "" {
		lines = append(lines,
			textVisibleCmd("Authorized user", "type "+scriptQuote(username)),
			textVisibleCmd("Authorized user", "key return"),
			textVisibleCmd("Authorized user", "wait-prompt-clear "+scriptQuote("Authorized user")),
			textVisibleCmd("Enter username", "type "+scriptQuote(username)),
			textVisibleCmd("Enter username", "key return"),
			textVisibleCmd("Enter username", "wait-prompt-clear "+scriptQuote("Enter username")+" 1s"),
			textVisibleCmd("user name", "type "+scriptQuote(username)),
			textVisibleCmd("user name", "key return"),
			textVisibleCmd("user name", "wait-prompt-clear "+scriptQuote("user name")+" 1s"),
			"wait 1s",
		)
	}
	if password != "" {
		lines = append(lines,
			textVisibleCmd("Password", "type-keycodes "+scriptQuote(password)),
			textVisibleCmd("Password", "key return"),
			textVisibleCmd("Password", "wait-prompt-clear "+scriptQuote("Password")),
			textVisibleCmd("Enter password", "type-keycodes "+scriptQuote(password)),
			textVisibleCmd("Enter password", "key return"),
			textVisibleCmd("Enter password", "wait-prompt-clear "+scriptQuote("Enter password")+" 1s"),
			textVisibleCmd("password for user", "type-keycodes "+scriptQuote(password)),
			textVisibleCmd("password for user", "key return"),
			textVisibleCmd("password for user", "wait-prompt-clear "+scriptQuote("password for user")+" 1s"),
			"wait 3s",
		)
	}
	lines = append(lines, textVisibleCmd(sipSuccessText(mode), "screenshot"))
	if reboot {
		lines = append(lines,
			textVisibleCmd(sipSuccessText(mode), "type reboot"),
			textVisibleCmd(sipSuccessText(mode), "key return"),
		)
	}
	lines = append(lines, "")
	return strings.Join(lines, "\n")
}

func sipConfirmVZScriptLines() []string {
	return []string{
		textVisibleCmd("Are you sure", "type-keycodes "+scriptQuote("y")),
		textVisibleCmd("Are you sure", "key return"),
		textVisibleCmd("Are you sure", "wait-prompt-clear "+scriptQuote("Are you sure")),
		textVisibleCmd("[y/n]", "type-keycodes "+scriptQuote("y")),
		textVisibleCmd("[y/n]", "key return"),
		textVisibleCmd("[y/n]", "wait-prompt-clear "+scriptQuote("[y/n]")),
		"wait 1s",
	}
}

func textVisibleCmd(text, cmd string) string {
	return "[text-visible:" + url.QueryEscape(text) + "] " + cmd
}

func scriptQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func writeVZScriptForSIP(vmDirectory, mode, script string) (string, error) {
	base := fmt.Sprintf("sip-%s.vzscript", mode)
	path := filepath.Join(vmDirectory, base)
	if err := os.WriteFile(path, []byte(script), 0644); err != nil {
		return "", err
	}
	return path, nil
}

func sipSuccessText(mode string) string {
	switch mode {
	case "disable":
		return "System Integrity Protection is off."
	case "enable":
		return "System Integrity Protection is on."
	default:
		return "System Integrity Protection"
	}
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
  enable-auto    Generate recovery vzscript automation for SIP enable
  disable        Create recovery disk and show instructions to disable SIP
  disable-auto   Generate recovery vzscript automation for SIP disable
  status         Check SIP status (queries running guest agent)
  create-disk    Create (or recreate) the recovery tools disk

Examples:
  vz-macos -vm macos-3 sip enable-auto -user user -password secret
  vz-macos -vm macos-3 sip disable-auto -password secret -confirm
  vz-macos -vm macos-3 sip enable-auto -no-reboot
  vz-macos -vm macos-3 run -recovery -no-resume -gui -unattended -usb ~/.vz/vms/macos-3/recovery-disk.img -boot-commands ~/.vz/vms/macos-3/sip-enable.vzscript
`
