package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
)

type securityStatus struct {
	SandboxLevel    string `json:"sandbox_level"`
	HostContainment bool   `json:"host_containment"`
	NetworkMode     string `json:"network_mode"`
	Clipboard       bool   `json:"clipboard"`
	AutoMount       bool   `json:"auto_mount_volumes"`
	AutoUpgrade     bool   `json:"auto_upgrade_agent"`
	VNC             bool   `json:"vnc"`
	DebugStub       bool   `json:"debug_stub"`
	HTTP            bool   `json:"http"`
}

func runSecurityCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, handleSecurityCommand(args, env.Stdout))
}

func handleSecurityCommand(args []string, out io.Writer) error {
	if len(args) > 0 && args[0] == "status" {
		args = args[1:]
	}
	fs := flag.NewFlagSet("security", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonFlag := fs.Bool("json", false, "emit JSON")
	fs.Usage = func() { printSecurityUsage(os.Stderr) }
	if err := parseFlagsOrHelp(fs, args); err != nil {
		if err == errFlagHelp {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: cove security status [-json]")
	}
	if err := applySandboxDefaults(); err != nil {
		return err
	}
	status := currentSecurityStatus()
	if *jsonFlag {
		data, err := json.MarshalIndent(status, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal security status: %w", err)
		}
		fmt.Fprintln(out, string(data))
		return nil
	}
	fmt.Fprintf(out, "sandbox: %s\n", status.SandboxLevel)
	fmt.Fprintf(out, "host containment: %v\n", status.HostContainment)
	fmt.Fprintf(out, "network: %s\n", status.NetworkMode)
	fmt.Fprintf(out, "clipboard: %v\n", status.Clipboard)
	fmt.Fprintf(out, "auto-mount volumes: %v\n", status.AutoMount)
	fmt.Fprintf(out, "auto-upgrade agent: %v\n", status.AutoUpgrade)
	fmt.Fprintf(out, "vnc: %v\n", status.VNC)
	fmt.Fprintf(out, "debug stub: %v\n", status.DebugStub)
	fmt.Fprintf(out, "http listener: %v\n", status.HTTP)
	return nil
}

func currentSecurityStatus() securityStatus {
	policy, err := currentSandboxPolicy()
	level := "default"
	contained := false
	if err == nil && policy.Active() {
		level = string(policy.Level)
		contained = policy.HostContainment()
	}
	return securityStatus{
		SandboxLevel:    level,
		HostContainment: contained,
		NetworkMode:     networkMode,
		Clipboard:       enableClipboard,
		AutoMount:       autoMountVolumes,
		AutoUpgrade:     autoUpgradeAgent,
		VNC:             vncEnabled(),
		DebugStub:       debugStubEnabled(),
		HTTP:            runHTTPAddr != "",
	}
}

func printSecurityUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove security status [-json]

Show the effective host-containment and host-escape feature policy for this
invocation. Use -host-containment with cove run for fail-closed research VMs.`)
}
