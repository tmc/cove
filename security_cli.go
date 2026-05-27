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

	"github.com/tmc/cove/internal/buildscratch"
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
	AppSandbox  bool                `json:"apple_app_sandbox"`
	HomeDir     string              `json:"home_dir"`
	TempDir     string              `json:"temp_dir"`
	VMRoot      string              `json:"vm_root"`
	UnixSocket  securityProbeCheck  `json:"unix_socket"`
	LoopbackTCP securityProbeCheck  `json:"loopback_tcp"`
	HelperIPC   securityProbeCheck  `json:"helper_ipc"`
	Subprocess  securityProbeCheck  `json:"subprocess"`
	VZStart     *securityProbeCheck `json:"vz_start,omitempty"`
}

type securityProbeCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Path    string `json:"path,omitempty"`
	Command string `json:"command,omitempty"`
	Message string `json:"message,omitempty"`
}

type securitySandboxProbeOptions struct {
	VZStartVMDir   string
	VZStartDisk    string
	VZStartLinux   bool
	VZStartKernel  string
	VZStartInitrd  string
	VZStartCmdline string
	VZStartTimeout time.Duration
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
	if len(args) > 0 && args[0] == "bookmark-probe" {
		return handleSecurityBookmarkProbeCommand(env, args[1:])
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

func handleSecurityBookmarkProbeCommand(env commandEnv, args []string) error {
	fs := flag.NewFlagSet("security bookmark-probe", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	jsonFlag := fs.Bool("json", false, "emit JSON")
	path := fs.String("path", "", "file to bookmark")
	fs.Usage = func() { printSecurityUsage(env.Stderr) }
	if err := parseFlagsOrHelp(fs, args); err != nil {
		if err == errFlagHelp {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: cove security bookmark-probe [-json] [-path file]")
	}
	target := *path
	cleanup := func() {}
	if target == "" {
		dir, err := os.MkdirTemp("", "cove-bookmark-probe-")
		if err != nil {
			return fmt.Errorf("create bookmark probe dir: %w", err)
		}
		cleanup = func() { _ = os.RemoveAll(dir) }
		target = filepath.Join(dir, "grant.txt")
		if err := os.WriteFile(target, []byte("cove security-scoped bookmark proof\n"), 0600); err != nil {
			cleanup()
			return fmt.Errorf("write bookmark probe file: %w", err)
		}
	}
	defer cleanup()

	report, err := securityScopedBookmarkRoundTrip(target)
	if err != nil {
		return err
	}
	if *jsonFlag {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal security bookmark probe: %w", err)
		}
		fmt.Fprintln(env.Stdout, string(data))
		return nil
	}
	fmt.Fprintf(env.Stdout, "apple app sandbox: %v\n", report.AppSandbox)
	fmt.Fprintf(env.Stdout, "path: %s\n", report.Path)
	fmt.Fprintf(env.Stdout, "resolved path: %s\n", report.ResolvedPath)
	fmt.Fprintf(env.Stdout, "bookmark bytes: %d\n", report.BookmarkSize)
	fmt.Fprintf(env.Stdout, "stale: %v\n", report.Stale)
	fmt.Fprintf(env.Stdout, "started access: %v\n", report.Started)
	fmt.Fprintf(env.Stdout, "read bytes: %d\n", report.ReadBytes)
	return nil
}

func handleSecuritySandboxProbeCommand(env commandEnv, args []string) error {
	fs := flag.NewFlagSet("security probe-sandbox", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	jsonFlag := fs.Bool("json", false, "emit JSON")
	var opts securitySandboxProbeOptions
	fs.StringVar(&opts.VZStartVMDir, "vz-start-vm-dir", "", "scratch VM directory for an optional VZ start/stop probe")
	fs.StringVar(&opts.VZStartDisk, "vz-start-disk", "", "scratch disk image for an optional VZ start/stop probe")
	fs.BoolVar(&opts.VZStartLinux, "vz-start-linux", false, "treat the optional VZ start/stop probe as a Linux VM")
	fs.StringVar(&opts.VZStartKernel, "vz-start-kernel", "", "Linux kernel for the optional VZ start/stop probe")
	fs.StringVar(&opts.VZStartInitrd, "vz-start-initrd", "", "Linux initrd for the optional VZ start/stop probe")
	fs.StringVar(&opts.VZStartCmdline, "vz-start-cmdline", "", "Linux kernel command line for the optional VZ start/stop probe")
	fs.DurationVar(&opts.VZStartTimeout, "vz-start-timeout", 30*time.Second, "timeout for the optional VZ start/stop probe")
	fs.Usage = func() { printSecurityUsage(env.Stderr) }
	if err := parseFlagsOrHelp(fs, args); err != nil {
		if err == errFlagHelp {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 || !opts.valid() {
		return fmt.Errorf("usage: cove security probe-sandbox [-json] [-vz-start-vm-dir dir -vz-start-disk disk]")
	}
	probe := currentSecuritySandboxProbe(opts)
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
	fmt.Fprintf(env.Stdout, "loopback tcp: %s", probe.LoopbackTCP.Status)
	if probe.LoopbackTCP.Message != "" {
		fmt.Fprintf(env.Stdout, " (%s)", probe.LoopbackTCP.Message)
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
	if probe.VZStart != nil {
		fmt.Fprintf(env.Stdout, "vz start: %s", probe.VZStart.Status)
		if probe.VZStart.Message != "" {
			fmt.Fprintf(env.Stdout, " (%s)", probe.VZStart.Message)
		}
		fmt.Fprintln(env.Stdout)
	}
	return nil
}

func (opts securitySandboxProbeOptions) valid() bool {
	if opts.VZStartVMDir == "" && opts.VZStartDisk == "" {
		return true
	}
	return opts.VZStartVMDir != "" && opts.VZStartDisk != ""
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
	return securityStatus{
		SandboxLevel:      level,
		HostContainment:   contained,
		AppleAppSandbox:   appSandbox.Active,
		AppleAppSandboxID: appSandbox.ContainerID,
		HomeDir:           homeDir,
		StateRoot:         vmconfig.StateDir(),
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

func currentSecuritySandboxProbe(opts securitySandboxProbeOptions) securitySandboxProbe {
	appSandbox := currentAppleAppSandboxStatus()
	homeDir, _ := os.UserHomeDir()
	vmRoot := vmconfig.BaseDir()
	probe := securitySandboxProbe{
		AppSandbox:  appSandbox.Active,
		HomeDir:     homeDir,
		TempDir:     os.TempDir(),
		VMRoot:      vmRoot,
		UnixSocket:  probeSandboxUnixSocket(vmRoot),
		LoopbackTCP: probeSandboxLoopbackTCP(),
		HelperIPC:   probeSandboxHelperIPC(),
		Subprocess:  probeSandboxSubprocess(),
	}
	if opts.VZStartVMDir != "" {
		check := probeSandboxVZStart(opts)
		probe.VZStart = &check
	}
	return probe
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

func probeSandboxLoopbackTCP() securityProbeCheck {
	check := securityProbeCheck{Name: "loopback-tcp"}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		check.Status = "fail"
		check.Message = err.Error()
		return check
	}
	defer ln.Close()
	check.Path = ln.Addr().String()

	accepted := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			accepted <- err
			return
		}
		_ = conn.Close()
		accepted <- nil
	}()

	conn, err := net.DialTimeout("tcp", ln.Addr().String(), 2*time.Second)
	if err != nil {
		check.Status = "fail"
		check.Message = fmt.Sprintf("dial: %v", err)
		return check
	}
	_ = conn.Close()
	select {
	case err := <-accepted:
		if err != nil {
			check.Status = "fail"
			check.Message = fmt.Sprintf("accept: %v", err)
			return check
		}
	case <-time.After(2 * time.Second):
		check.Status = "fail"
		check.Message = "accept timeout"
		return check
	}
	check.Status = "pass"
	check.Message = "bound and accepted"
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

var probeSandboxVZStartGuest = startScratchBuildGuest

func probeSandboxVZStart(opts securitySandboxProbeOptions) securityProbeCheck {
	check := securityProbeCheck{Name: "vz-start", Path: opts.VZStartVMDir}
	if opts.VZStartTimeout <= 0 {
		opts.VZStartTimeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), opts.VZStartTimeout)
	defer cancel()

	cleanup, err := withSandboxVZStartGlobals(opts, func() (buildGuestCleanup, error) {
		return probeSandboxVZStartGuest(ctx, buildscratch.Scratch{
			ID:       "sandbox-probe",
			Dir:      opts.VZStartVMDir,
			DiskPath: opts.VZStartDisk,
			Created:  time.Now(),
		})
	})
	if err != nil {
		check.Status = "fail"
		check.Message = err.Error()
		return check
	}
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cleanupCancel()
	if err := cleanup(cleanupCtx); err != nil {
		check.Status = "fail"
		check.Message = fmt.Sprintf("cleanup: %v", err)
		return check
	}
	check.Status = "pass"
	check.Message = "started and stopped"
	return check
}

func withSandboxVZStartGlobals(opts securitySandboxProbeOptions, fn func() (buildGuestCleanup, error)) (buildGuestCleanup, error) {
	oldLinuxMode := linuxMode
	oldWindowsMode := windowsMode
	oldKernelPath := kernelPath
	oldInitrdPath := initrdPath
	oldCmdLine := cmdLine
	oldStartTimeout := startTimeout
	oldAttachment := runtimeSystemDiskAttachment
	oldClipboard := enableClipboard
	defer func() {
		linuxMode = oldLinuxMode
		windowsMode = oldWindowsMode
		kernelPath = oldKernelPath
		initrdPath = oldInitrdPath
		cmdLine = oldCmdLine
		startTimeout = oldStartTimeout
		runtimeSystemDiskAttachment = oldAttachment
		enableClipboard = oldClipboard
	}()

	linuxMode = opts.VZStartLinux
	windowsMode = false
	kernelPath = opts.VZStartKernel
	initrdPath = opts.VZStartInitrd
	cmdLine = opts.VZStartCmdline
	startTimeout = opts.VZStartTimeout
	enableClipboard = false
	runtimeSystemDiskAttachment = systemDiskAttachmentDiskImage
	return fn()
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
       cove security bookmark-probe [-json] [-path file]
       cove security probe-sandbox [-json]

Show the effective host-containment and host-escape feature policy for this
invocation. Use -host-containment with cove run for fail-closed research VMs.`)
}
