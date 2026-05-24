package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/tmc/cove/internal/controlserver"
	"github.com/tmc/cove/internal/vmconfig"
)

type statusOptions struct {
	VM string
}

func statusCommand(env commandEnv, args ...string) error {
	env = env.withDefaultIO()
	opts, err := parseStatusArgs(env, args)
	if errors.Is(err, errFlagHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	targetDir, err := resolveStatusVMDir(opts.VM)
	if err != nil {
		return err
	}
	if !isVMRunningAt(targetDir) {
		state := detectVMState(targetDir)
		if state == "starting" {
			_, note := runtimeListFields(targetDir, state)
			fmt.Fprintf(env.Stdout, "starting: %s\n", note)
			return nil
		}
		name := filepath.Base(targetDir)
		return fmt.Errorf("vm %q is %s; status requires a running VM\n  start it with: cove -vm %s run\n  list VMs with: cove list", name, state, name)
	}
	client := NewControlClient(GetControlSocketPathForVM(targetDir))
	osName, err := detectGuestOS(client)
	if err != nil {
		return err
	}

	status := controlserver.AgentHealthState{DaemonStatus: "connected", UserStatus: "unknown"}
	if agentStatus, err := client.AgentExecTypedTimeout([]string{"true"}, nil, "", 5*time.Second); err != nil || agentStatus.GetExitCode() != 0 {
		status.DaemonStatus = "disconnected"
	}
	if status.DaemonStatus == "connected" {
		switch osName {
		case guestOSLinux:
			session, ok, err := probeLinuxGUISessionControl(client)
			if err != nil {
				return err
			}
			status.GUISession = session
			status.GUISessionActive = ok
		case guestOSDarwin:
			session, ok, err := probeMacOSGUISessionControl(client)
			if err != nil {
				return err
			}
			status.GUISession = session
			status.GUISessionActive = ok
		}
	}
	fmt.Fprintln(env.Stdout, controlserver.AgentHealthSummary(status))
	return nil
}

func parseStatusArgs(env commandEnv, args []string) (statusOptions, error) {
	env = env.withDefaultIO()
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	vmFlag := fs.String("vm", "", "VM name")
	fs.Usage = func() { printStatusUsage(env.Stdout) }
	if err := parseFlagsOrHelp(fs, args); err != nil {
		return statusOptions{}, err
	}
	if fs.NArg() > 1 {
		return statusOptions{}, fmt.Errorf("usage: cove status [-vm name] [vm]")
	}
	target := ""
	vmFlagSet := flagWasProvided(fs, "vm")
	if vmFlagSet {
		target = strings.TrimSpace(*vmFlag)
	}
	if target == "" && fs.NArg() == 0 {
		target = strings.TrimSpace(vmName)
	}
	if fs.NArg() == 1 {
		positional := fs.Arg(0)
		if vmFlagSet && target != "" && target != positional {
			return statusOptions{}, fmt.Errorf("status: -vm %q does not match positional VM %q", target, positional)
		}
		target = positional
	}
	return statusOptions{VM: target}, nil
}

func printStatusUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove status [-vm name] [vm]

Show guest-agent and GUI-session status for a running VM.
If no VM is named, cove uses the active VM.`)
}

func resolveStatusVMDir(name string) (string, error) {
	if strings.TrimSpace(name) != "" {
		return requireExistingVMForControl(name)
	}
	if strings.TrimSpace(vmDir) != "" && vmconfig.Validate(vmDir) {
		return vmDir, nil
	}
	return requireExistingVMForControl(vmconfig.ActiveName())
}

func probeLinuxGUISessionControl(client *ControlClient) (controlserver.GUISession, bool, error) {
	resp, err := client.AgentExecTypedTimeout([]string{"loginctl", "list-sessions", "--output", "json", "--no-pager"}, nil, "", 5*time.Second)
	if err != nil {
		return controlserver.GUISession{}, false, fmt.Errorf("query gui sessions: %w", err)
	}
	if resp.GetExitCode() != 0 {
		msg := strings.TrimSpace(resp.GetStderr())
		if msg == "" {
			msg = strings.TrimSpace(resp.GetStdout())
		}
		return controlserver.GUISession{}, false, fmt.Errorf("query gui sessions: %s", msg)
	}
	rows, err := parseLinuxLoginctlSessionRows([]byte(resp.GetStdout()))
	if err != nil {
		return controlserver.GUISession{}, false, err
	}
	if session, ok := activeGraphicalLoginctlSession(rows); ok {
		return session, true, nil
	}
	for _, row := range rows {
		if row.State != "active" || row.ID == "" {
			continue
		}
		show, err := client.AgentExecTypedTimeout([]string{"loginctl", "show-session", row.ID, "-p", "Name", "-p", "User", "-p", "Seat", "-p", "State", "-p", "Type", "--no-pager"}, nil, "", 5*time.Second)
		if err != nil || show.GetExitCode() != 0 {
			continue
		}
		if session, ok := parseLoginctlShowGUISession(row.ID, show.GetStdout()); ok {
			return session, true, nil
		}
	}
	return controlserver.GUISession{}, false, nil
}

func probeMacOSGUISessionControl(client *ControlClient) (controlserver.GUISession, bool, error) {
	resp, err := client.AgentExecTypedTimeout([]string{"sh", "-lc", macOSGUISessionScript()}, nil, "", 5*time.Second)
	if err != nil {
		return controlserver.GUISession{}, false, fmt.Errorf("query gui session: %w", err)
	}
	if resp.GetExitCode() != 0 {
		return controlserver.GUISession{}, false, nil
	}
	user := strings.TrimSpace(resp.GetStdout())
	if user == "" {
		return controlserver.GUISession{}, false, nil
	}
	return controlserver.GUISession{User: user, Seat: "console", Kind: "console"}, true, nil
}
