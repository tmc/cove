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
	return commandError(env, handleSecurityCommand(env, args))
}

func handleSecurityCommand(env commandEnv, args []string) error {
	if env.Stdout == nil {
		env.Stdout = os.Stdout
	}
	if env.Stderr == nil {
		env.Stderr = os.Stderr
	}
	if len(args) > 0 && args[0] == "status" {
		args = args[1:]
	}
	fs := flag.NewFlagSet("security", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	jsonFlag := fs.Bool("json", false, "emit JSON")
	fs.Usage = func() { printSecurityUsage(env.Stderr) }
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
		fmt.Fprintln(env.Stdout, string(data))
		return nil
	}
	fmt.Fprintf(env.Stdout, "sandbox: %s\n", status.SandboxLevel)
	fmt.Fprintf(env.Stdout, "host containment: %v\n", status.HostContainment)
	fmt.Fprintf(env.Stdout, "network: %s\n", status.NetworkMode)
	fmt.Fprintf(env.Stdout, "clipboard: %v\n", status.Clipboard)
	fmt.Fprintf(env.Stdout, "auto-mount volumes: %v\n", status.AutoMount)
	fmt.Fprintf(env.Stdout, "auto-upgrade agent: %v\n", status.AutoUpgrade)
	fmt.Fprintf(env.Stdout, "vnc: %v\n", status.VNC)
	fmt.Fprintf(env.Stdout, "debug stub: %v\n", status.DebugStub)
	fmt.Fprintf(env.Stdout, "http listener: %v\n", status.HTTP)
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
