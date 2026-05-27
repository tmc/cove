package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/tmc/cove/internal/vmconfig"
)

type securityStatus struct {
	SandboxLevel      string `json:"sandbox_level"`
	HostContainment   bool   `json:"host_containment"`
	AppleAppSandbox   bool   `json:"apple_app_sandbox"`
	AppleAppSandboxID string `json:"apple_app_sandbox_id,omitempty"`
	HomeDir           string `json:"home_dir"`
	StateRoot         string `json:"state_root"`
	VMRoot            string `json:"vm_root"`
	NetworkMode       string `json:"network_mode"`
	Clipboard         bool   `json:"clipboard"`
	AutoMount         bool   `json:"auto_mount_volumes"`
	AutoUpgrade       bool   `json:"auto_upgrade_agent"`
	VNC               bool   `json:"vnc"`
	DebugStub         bool   `json:"debug_stub"`
	HTTP              bool   `json:"http"`
}

type securitySandboxProbe struct {
	AppSandbox bool               `json:"apple_app_sandbox"`
	HomeDir    string             `json:"home_dir"`
	TempDir    string             `json:"temp_dir"`
	VMRoot     string             `json:"vm_root"`
	UnixSocket securityProbeCheck `json:"unix_socket"`
	HelperIPC  securityProbeCheck `json:"helper_ipc"`
	Subprocess securityProbeCheck `json:"subprocess"`
}

type securityProbeCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Path    string `json:"path,omitempty"`
	Command string `json:"command,omitempty"`
	Message string `json:"message,omitempty"`
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
	if len(args) > 0 && args[0] == "probe-sandbox" {
		return handleSecuritySandboxProbeCommand(env, args[1:])
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
	fmt.Fprintf(env.Stdout, "apple app sandbox: %v\n", status.AppleAppSandbox)
	if status.AppleAppSandboxID != "" {
		fmt.Fprintf(env.Stdout, "apple app sandbox id: %s\n", status.AppleAppSandboxID)
	}
	fmt.Fprintf(env.Stdout, "home: %s\n", status.HomeDir)
	fmt.Fprintf(env.Stdout, "state root: %s\n", status.StateRoot)
	fmt.Fprintf(env.Stdout, "vm root: %s\n", status.VMRoot)
	fmt.Fprintf(env.Stdout, "network: %s\n", status.NetworkMode)
	fmt.Fprintf(env.Stdout, "clipboard: %v\n", status.Clipboard)
	fmt.Fprintf(env.Stdout, "auto-mount volumes: %v\n", status.AutoMount)
	fmt.Fprintf(env.Stdout, "auto-upgrade agent: %v\n", status.AutoUpgrade)
	fmt.Fprintf(env.Stdout, "vnc: %v\n", status.VNC)
	fmt.Fprintf(env.Stdout, "debug stub: %v\n", status.DebugStub)
	fmt.Fprintf(env.Stdout, "http listener: %v\n", status.HTTP)
	return nil
}

func handleSecuritySandboxProbeCommand(env commandEnv, args []string) error {
	fs := flag.NewFlagSet("security probe-sandbox", flag.ContinueOnError)
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
		return fmt.Errorf("usage: cove security probe-sandbox [-json]")
	}
	probe := currentSecuritySandboxProbe()
	if *jsonFlag {
		data, err := json.MarshalIndent(probe, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal security sandbox probe: %w", err)
		}
		fmt.Fprintln(env.Stdout, string(data))
		return nil
	}
	fmt.Fprintf(env.Stdout, "apple app sandbox: %v\n", probe.AppSandbox)
	fmt.Fprintf(env.Stdout, "home: %s\n", probe.HomeDir)
	fmt.Fprintf(env.Stdout, "temp: %s\n", probe.TempDir)
	fmt.Fprintf(env.Stdout, "vm root: %s\n", probe.VMRoot)
	fmt.Fprintf(env.Stdout, "unix socket: %s", probe.UnixSocket.Status)
	if probe.UnixSocket.Message != "" {
		fmt.Fprintf(env.Stdout, " (%s)", probe.UnixSocket.Message)
	}
	fmt.Fprintln(env.Stdout)
	fmt.Fprintf(env.Stdout, "helper ipc: %s", probe.HelperIPC.Status)
	if probe.HelperIPC.Message != "" {
		fmt.Fprintf(env.Stdout, " (%s)", probe.HelperIPC.Message)
	}
	fmt.Fprintln(env.Stdout)
	fmt.Fprintf(env.Stdout, "subprocess: %s", probe.Subprocess.Status)
	if probe.Subprocess.Message != "" {
		fmt.Fprintf(env.Stdout, " (%s)", probe.Subprocess.Message)
	}
	fmt.Fprintln(env.Stdout)
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
	appSandbox := currentAppleAppSandboxStatus()
	homeDir, _ := os.UserHomeDir()
	stateRoot := ""
	if homeDir != "" {
		stateRoot = filepath.Join(homeDir, ".vz")
	}
	return securityStatus{
		SandboxLevel:      level,
		HostContainment:   contained,
		AppleAppSandbox:   appSandbox.Active,
		AppleAppSandboxID: appSandbox.ContainerID,
		HomeDir:           homeDir,
		StateRoot:         stateRoot,
		VMRoot:            vmconfig.BaseDir(),
		NetworkMode:       networkMode,
		Clipboard:         enableClipboard,
		AutoMount:         autoMountVolumes,
		AutoUpgrade:       autoUpgradeAgent,
		VNC:               vncEnabled(),
		DebugStub:         debugStubEnabled(),
		HTTP:              runHTTPAddr != "",
	}
}

func currentSecuritySandboxProbe() securitySandboxProbe {
	appSandbox := currentAppleAppSandboxStatus()
	homeDir, _ := os.UserHomeDir()
	vmRoot := vmconfig.BaseDir()
	return securitySandboxProbe{
		AppSandbox: appSandbox.Active,
		HomeDir:    homeDir,
		TempDir:    os.TempDir(),
		VMRoot:     vmRoot,
		UnixSocket: probeSandboxUnixSocket(vmRoot),
		HelperIPC:  probeSandboxHelperIPC(),
		Subprocess: probeSandboxSubprocess(),
	}
}

func probeSandboxUnixSocket(vmRoot string) securityProbeCheck {
	dir := filepath.Join(vmRoot, fmt.Sprintf(".p%d", os.Getpid()))
	path := filepath.Join(dir, "c.sock")
	check := securityProbeCheck{Name: "unix-socket", Path: path}
	if err := os.MkdirAll(dir, 0700); err != nil {
		check.Status = "fail"
		check.Message = fmt.Sprintf("create probe dir: %v", err)
		return check
	}
	defer os.RemoveAll(dir)
	ln, err := net.Listen("unix", path)
	if err != nil {
		check.Status = "fail"
		check.Message = err.Error()
		return check
	}
	_ = ln.Close()
	check.Status = "pass"
	check.Message = "bound and closed"
	return check
}

var probeSandboxDialHelper = dialHelper

func probeSandboxHelperIPC() securityProbeCheck {
	check := securityProbeCheck{Name: "helper-ipc", Path: helperSocketPath}
	conn, err := probeSandboxDialHelper()
	if err != nil {
		switch {
		case errors.Is(err, errHelperUnavailable):
			check.Status = "skip"
			check.Message = "helper socket not present"
		case errors.Is(err, os.ErrPermission):
			check.Status = "blocked"
			check.Message = err.Error()
		default:
			check.Status = "fail"
			check.Message = err.Error()
		}
		return check
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err := json.NewEncoder(conn).Encode(helperRequest{Op: "ping"}); err != nil {
		check.Status = "fail"
		check.Message = fmt.Sprintf("send ping: %v", err)
		return check
	}
	var resp helperResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		check.Status = "fail"
		check.Message = fmt.Sprintf("read ping response: %v", err)
		return check
	}
	if !resp.OK {
		check.Status = "fail"
		check.Message = fmt.Sprintf("helper: %s", resp.Error)
		return check
	}
	check.Status = "pass"
	check.Message = "ping ok"
	return check
}

func probeSandboxSubprocess() securityProbeCheck {
	const cmdPath = "/usr/bin/hdiutil"
	check := securityProbeCheck{Name: "subprocess", Command: cmdPath + " info"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, cmdPath, "info").CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		check.Status = "fail"
		check.Message = "timeout"
		return check
	}
	if err != nil {
		check.Status = "fail"
		check.Message = fmt.Sprintf("%v: %s", err, firstProbeOutputLine(string(out)))
		return check
	}
	check.Status = "pass"
	check.Message = "executed"
	return check
}

func firstProbeOutputLine(s string) string {
	for i, r := range s {
		if r == '\n' || r == '\r' {
			return s[:i]
		}
	}
	return s
}

func printSecurityUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove security status [-json]
       cove security probe-sandbox [-json]

Show the effective host-containment and host-escape feature policy for this
invocation. Use -host-containment with cove run for fail-closed research VMs.`)
}
