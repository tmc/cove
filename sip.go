package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	agentstate "github.com/tmc/vz-macos/internal/agent"
)

// handleSIPCommand dispatches SIP management subcommands.
func handleSIPCommand(args []string) error {
	if len(args) == 0 {
		fmt.Print(sipUsage)
		return nil
	}

	switch args[0] {
	case "enable":
		if err := requireMacOSSIPTarget(); err != nil {
			return err
		}
		return sipEnable()
	case "enable-auto":
		if err := requireMacOSSIPTarget(); err != nil {
			return err
		}
		return sipAuto("enable", args[1:])
	case "disable":
		if err := requireMacOSSIPTarget(); err != nil {
			return err
		}
		return sipDisable()
	case "disable-auto":
		if err := requireMacOSSIPTarget(); err != nil {
			return err
		}
		return sipAuto("disable", args[1:])
	case "status":
		if err := requireMacOSSIPTarget(); err != nil {
			return err
		}
		return sipStatus()
	case "create-disk":
		if err := requireMacOSSIPTarget(); err != nil {
			return err
		}
		return sipCreateDisk()
	case "help":
		fmt.Print(sipUsage)
		return nil
	default:
		return fmt.Errorf("unknown sip command: %s\n\n%s", args[0], sipUsage)
	}
}

func requireMacOSSIPTarget() error {
	if agentstate.Platform(vmDir) == agentstate.PlatformLinux {
		return fmt.Errorf("sip is only supported for macOS VMs")
	}
	return nil
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
	fmt.Printf("     cove -vm %s run -recovery -gui -usb %q\n", sipTargetVMName(), path)
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
	fmt.Printf("     cove -vm %s run -recovery -gui -usb %q\n", sipTargetVMName(), path)
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
	if err := fs.Parse(args); err != nil {
		return err
	}

	recoveryPath, err := EnsureRecoveryDisk(vmDir)
	if err != nil {
		return fmt.Errorf("create recovery disk: %w", err)
	}

	scriptData, err := generateSIPVZScript(mode, *user, *pass)
	if err != nil {
		return fmt.Errorf("generate sip automation script: %w", err)
	}
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
	fmt.Printf("  cove -vm %s run -recovery -no-resume -gui -unattended -usb %q -boot-commands %q\n",
		sipTargetVMName(), recoveryPath, bootCmdsPath)
	fmt.Println()
	fmt.Printf("After reboot, verify with:\n  cove -vm %s sip status\n", sipTargetVMName())
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
	fmt.Printf("  cove -vm %s ctl -wait 60s agent-exec csrutil status\n", sipTargetVMName())
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

func generateSIPVZScript(mode, username, password string) (string, error) {
	data, err := builtinScripts.ReadFile("vzscripts/sip-" + mode + ".vzscript")
	if err != nil {
		return "", fmt.Errorf("read sip %s script: %w", mode, err)
	}
	var b strings.Builder
	if username != "" {
		fmt.Fprintf(&b, "env SIP_USER=%s\n", scriptQuote(username))
	}
	if password != "" {
		fmt.Fprintf(&b, "env SIP_PASSWORD=%s\n", scriptQuote(password))
	}
	b.Write(data)
	return b.String(), nil
}

func writeVZScriptForSIP(vmDirectory, mode, script string) (string, error) {
	base := fmt.Sprintf("sip-%s.vzscript", mode)
	path := filepath.Join(vmDirectory, base)
	if err := os.WriteFile(path, []byte(script), 0644); err != nil {
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

const sipUsage = `Usage: cove sip <command>

Commands:
  enable         Create recovery disk and show instructions to enable SIP
  enable-auto    Generate recovery vzscript automation for SIP enable
  disable        Create recovery disk and show instructions to disable SIP
  disable-auto   Generate recovery vzscript automation for SIP disable
  status         Check SIP status (queries running guest agent)
  create-disk    Create (or recreate) the recovery tools disk

Examples:
  cove -vm macos-3 sip enable-auto -user user -password secret
  cove -vm macos-3 sip disable-auto -password secret
  cove -vm macos-3 run -recovery -no-resume -gui -unattended -usb ~/.vz/vms/macos-3/recovery-disk.img -boot-commands ~/.vz/vms/macos-3/sip-enable.vzscript
`
