package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tmc/cove/internal/vmconfig"
	"golang.org/x/sys/unix"
)

type runWorkerProbeReport struct {
	Action            string               `json:"action"`
	ParentAppSandbox  bool                 `json:"parent_apple_app_sandbox"`
	Child             runWorkerChildReport `json:"child"`
	Message           string               `json:"message,omitempty"`
	ChildOutputPrefix string               `json:"child_output_prefix,omitempty"`
}

type runWorkerChildReport struct {
	Action      string `json:"action"`
	AppSandbox  bool   `json:"apple_app_sandbox"`
	ContainerID string `json:"apple_app_sandbox_id,omitempty"`
	HomeDir     string `json:"home_dir"`
	ReceivedFD  bool   `json:"received_fd"`
	ReceivedFDs int    `json:"received_fds"`
	Bytes       int    `json:"bytes"`
	SHA256      string `json:"sha256"`
	Command     string `json:"handoff_command,omitempty"`
	VMName      string `json:"vm_name,omitempty"`
	VMDir       string `json:"vm_dir,omitempty"`
	ResolvedDir string `json:"resolved_dir,omitempty"`
	OSType      string `json:"os_type,omitempty"`
	State       string `json:"state,omitempty"`
	ConfigRead  bool   `json:"config_read,omitempty"`
	RuntimeRead bool   `json:"runtime_read,omitempty"`
	Stale       bool   `json:"bookmark_stale,omitempty"`
	BookmarkKey string `json:"bookmark_key,omitempty"`
	BookmarkLen int    `json:"bookmark_bytes,omitempty"`
	Message     string `json:"message,omitempty"`
}

func runWorkerCommand(env commandEnv, _ string, args []string) int {
	return commandError(env, handleRunWorkerCommand(env.WithDefaultIO(), args))
}

func handleRunWorkerCommand(env commandEnv, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cove __run-worker <probe|child>")
	}
	switch args[0] {
	case "probe":
		return runWorkerProbeCommand(env, args[1:])
	case "status-preflight":
		return runWorkerStatusPreflightCommand(env, args[1:])
	case "child":
		return runWorkerChildCommand(env, args[1:])
	default:
		return fmt.Errorf("unknown __run-worker command: %s", args[0])
	}
}

func runWorkerProbeCommand(env commandEnv, args []string) error {
	fs := flag.NewFlagSet("__run-worker probe", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	jsonFlag := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: cove __run-worker probe [-json]")
	}

	report, err := runWorkerProbe()
	if err != nil {
		return err
	}
	if *jsonFlag {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal run-worker probe: %w", err)
		}
		fmt.Fprintln(env.Stdout, string(data))
		return nil
	}
	fmt.Fprintf(env.Stdout, "run-worker: %s\n", report.Message)
	fmt.Fprintf(env.Stdout, "child apple app sandbox: %v\n", report.Child.AppSandbox)
	fmt.Fprintf(env.Stdout, "fd bytes: %d\n", report.Child.Bytes)
	return nil
}

func runWorkerStatusPreflightCommand(env commandEnv, args []string) error {
	fs := flag.NewFlagSet("__run-worker status-preflight", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	jsonFlag := fs.Bool("json", false, "emit JSON")
	vmFlag := fs.String("vm", "", "VM name")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	name := strings.TrimSpace(*vmFlag)
	if fs.NArg() == 1 {
		if name != "" && name != fs.Arg(0) {
			return fmt.Errorf("status-preflight: -vm %q does not match positional VM %q", name, fs.Arg(0))
		}
		name = fs.Arg(0)
	}
	if fs.NArg() > 1 || name == "" {
		return fmt.Errorf("usage: cove __run-worker status-preflight [-json] -vm name")
	}

	report, err := runWorkerStatusPreflight(name)
	if err != nil {
		return err
	}
	if *jsonFlag {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal run-worker status preflight: %w", err)
		}
		fmt.Fprintln(env.Stdout, string(data))
		return nil
	}
	fmt.Fprintf(env.Stdout, "run-worker status preflight: %s %s\n", report.Child.VMName, report.Child.State)
	return nil
}

func runWorkerProbe() (runWorkerProbeReport, error) {
	root, err := runWorkerContainerTempDir()
	if err != nil {
		return runWorkerProbeReport{}, err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return runWorkerProbeReport{}, fmt.Errorf("create run-worker temp dir: %w", err)
	}
	dir, err := os.MkdirTemp(root, "cove-run-worker-")
	if err != nil {
		return runWorkerProbeReport{}, fmt.Errorf("create run-worker workspace: %w", err)
	}
	defer os.RemoveAll(dir)

	payload := []byte("cove app sandbox run-worker explicit descriptor proof\n")
	sum := sha256.Sum256(payload)
	want := hex.EncodeToString(sum[:])
	grantPath := filepath.Join(dir, "grant.txt")
	if err := os.WriteFile(grantPath, payload, 0o600); err != nil {
		return runWorkerProbeReport{}, fmt.Errorf("write descriptor grant: %w", err)
	}
	file, err := os.Open(grantPath)
	if err != nil {
		return runWorkerProbeReport{}, fmt.Errorf("open descriptor grant: %w", err)
	}
	defer file.Close()
	handoff := runWorkerHandoff{
		Version: runWorkerHandoffVersion,
		Command: "probe",
		VM: runWorkerHandoffVM{
			Name: "sandbox-worker-probe",
			Dir:  dir,
		},
		FDs: []runWorkerHandoffFD{{
			Name:   "grant",
			Index:  0,
			Path:   grantPath,
			Mode:   "read",
			SHA256: want,
		}},
		Bookmarks: []runWorkerHandoffBookmark{{
			Key:   "vm:sandbox-worker-probe",
			Kind:  "vm",
			Path:  dir,
			Bytes: []byte("cove app-scope bookmark placeholder"),
		}},
	}

	sockPath := filepath.Join(dir, "rw.sock")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: sockPath, Net: "unix"})
	if err != nil {
		return runWorkerProbeReport{}, fmt.Errorf("listen run-worker socket: %w", err)
	}
	defer ln.Close()

	sendErr := make(chan error, 1)
	go func() {
		sendErr <- sendRunWorkerHandoff(ln, handoff, []*os.File{file}, 45*time.Second)
	}()

	exe, err := os.Executable()
	if err != nil {
		return runWorkerProbeReport{}, fmt.Errorf("find executable: %w", err)
	}
	cmd := exec.Command(exe, "__run-worker", "child", "-sock", sockPath)
	cmd.Env = runWorkerChildEnv(os.Environ())
	out, childErr := cmd.CombinedOutput()

	if err := <-sendErr; err != nil {
		return runWorkerProbeReport{}, err
	}
	if childErr != nil {
		return runWorkerProbeReport{}, fmt.Errorf("run sandboxed worker: %w: %s", childErr, strings.TrimSpace(string(out)))
	}
	childJSON, err := firstJSONObjectBytes(out)
	if err != nil {
		return runWorkerProbeReport{}, fmt.Errorf("parse sandboxed worker output: %w: %s", err, strings.TrimSpace(string(out)))
	}
	var child runWorkerChildReport
	if err := json.Unmarshal(childJSON, &child); err != nil {
		return runWorkerProbeReport{}, fmt.Errorf("decode sandboxed worker output: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if !child.AppSandbox {
		return runWorkerProbeReport{}, fmt.Errorf("sandboxed worker did not report Apple App Sandbox")
	}
	if !child.ReceivedFD || child.SHA256 != want || child.Command != handoff.Command || child.VMName != handoff.VM.Name {
		return runWorkerProbeReport{}, fmt.Errorf("sandboxed worker descriptor proof failed: %+v", child)
	}
	return runWorkerProbeReport{
		Action:           "probe",
		ParentAppSandbox: appleAppSandboxActive(),
		Child:            child,
		Message:          "sandboxed worker received and read explicit descriptor",
	}, nil
}

func runWorkerStatusPreflight(name string) (runWorkerProbeReport, error) {
	storePath, err := defaultSecurityBookmarkStorePath()
	if err != nil {
		return runWorkerProbeReport{}, err
	}
	key := "vm:" + strings.TrimSpace(name)
	entry, bookmark, err := readSecurityBookmarkBytesFromStore(storePath, key)
	if err != nil {
		return runWorkerProbeReport{}, err
	}

	root, err := runWorkerContainerTempDir()
	if err != nil {
		return runWorkerProbeReport{}, err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return runWorkerProbeReport{}, fmt.Errorf("create run-worker temp dir: %w", err)
	}
	dir, err := os.MkdirTemp(root, "cove-run-worker-status-")
	if err != nil {
		return runWorkerProbeReport{}, fmt.Errorf("create run-worker workspace: %w", err)
	}
	defer os.RemoveAll(dir)

	handoff := runWorkerHandoff{
		Version: runWorkerHandoffVersion,
		Command: "status-preflight",
		VM: runWorkerHandoffVM{
			Name: name,
			Dir:  entry.Path,
		},
		Bookmarks: []runWorkerHandoffBookmark{{
			Key:   key,
			Kind:  "vm",
			Path:  entry.Path,
			Bytes: bookmark,
		}},
	}
	sockPath := filepath.Join(dir, "rw.sock")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: sockPath, Net: "unix"})
	if err != nil {
		return runWorkerProbeReport{}, fmt.Errorf("listen run-worker socket: %w", err)
	}
	defer ln.Close()

	sendErr := make(chan error, 1)
	go func() {
		sendErr <- sendRunWorkerHandoff(ln, handoff, nil, 45*time.Second)
	}()

	exe, err := os.Executable()
	if err != nil {
		return runWorkerProbeReport{}, fmt.Errorf("find executable: %w", err)
	}
	cmd := exec.Command(exe, "__run-worker", "child", "-sock", sockPath)
	cmd.Env = runWorkerChildEnv(os.Environ())
	out, childErr := cmd.CombinedOutput()

	if err := <-sendErr; err != nil {
		return runWorkerProbeReport{}, err
	}
	if childErr != nil {
		return runWorkerProbeReport{}, fmt.Errorf("run sandboxed worker: %w: %s", childErr, strings.TrimSpace(string(out)))
	}
	childJSON, err := firstJSONObjectBytes(out)
	if err != nil {
		return runWorkerProbeReport{}, fmt.Errorf("parse sandboxed worker output: %w: %s", err, strings.TrimSpace(string(out)))
	}
	var child runWorkerChildReport
	if err := json.Unmarshal(childJSON, &child); err != nil {
		return runWorkerProbeReport{}, fmt.Errorf("decode sandboxed worker output: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if !child.AppSandbox {
		return runWorkerProbeReport{}, fmt.Errorf("sandboxed worker did not report Apple App Sandbox")
	}
	if child.Command != handoff.Command || child.VMName != handoff.VM.Name || child.State == "" {
		return runWorkerProbeReport{}, fmt.Errorf("sandboxed worker status preflight failed: %+v", child)
	}
	return runWorkerProbeReport{
		Action:           "status-preflight",
		ParentAppSandbox: appleAppSandboxActive(),
		Child:            child,
		Message:          "sandboxed worker resolved VM bookmark and read metadata",
	}, nil
}

func runWorkerChildCommand(env commandEnv, args []string) error {
	fs := flag.NewFlagSet("__run-worker child", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	sockPath := fs.String("sock", "", "Unix socket for descriptor handoff")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 || *sockPath == "" {
		return fmt.Errorf("usage: cove __run-worker child -sock path")
	}

	report, err := runWorkerChild(*sockPath)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal run-worker child: %w", err)
	}
	fmt.Fprintln(env.Stdout, string(data))
	return nil
}

func runWorkerChild(sockPath string) (runWorkerChildReport, error) {
	status := currentAppleAppSandboxStatus()
	if !status.Active {
		return runWorkerChildReport{}, fmt.Errorf("run-worker child requires Apple App Sandbox")
	}
	handoff, files, err := receiveRunWorkerHandoff(sockPath, 45*time.Second)
	if err != nil {
		return runWorkerChildReport{}, err
	}
	defer closeRunWorkerFiles(files)
	switch handoff.Command {
	case "probe":
		return runWorkerProbeChild(status, handoff, files)
	case "status-preflight":
		return runWorkerStatusPreflightChild(status, handoff)
	default:
		return runWorkerChildReport{}, fmt.Errorf("unknown run-worker handoff command %q", handoff.Command)
	}
}

func runWorkerProbeChild(status appleAppSandboxStatus, handoff runWorkerHandoff, files []*os.File) (runWorkerChildReport, error) {
	grant, ok := handoff.fd("grant")
	if !ok {
		return runWorkerChildReport{}, fmt.Errorf("run-worker handoff missing grant descriptor")
	}
	if grant.Index >= len(files) {
		return runWorkerChildReport{}, fmt.Errorf("run-worker handoff grant index %d, received %d descriptors", grant.Index, len(files))
	}
	file := files[grant.Index]

	payload, err := io.ReadAll(file)
	if err != nil {
		return runWorkerChildReport{}, fmt.Errorf("read descriptor grant: %w", err)
	}
	sum := sha256.Sum256(payload)
	got := hex.EncodeToString(sum[:])
	if got != grant.SHA256 {
		return runWorkerChildReport{}, fmt.Errorf("descriptor grant sha256 = %s, want %s", got, grant.SHA256)
	}
	bookmark := runWorkerHandoffBookmark{}
	if len(handoff.Bookmarks) > 0 {
		bookmark = handoff.Bookmarks[0]
	}
	home, _ := os.UserHomeDir()
	return runWorkerChildReport{
		Action:      "child",
		AppSandbox:  status.Active,
		ContainerID: status.ContainerID,
		HomeDir:     home,
		ReceivedFD:  true,
		ReceivedFDs: len(files),
		Bytes:       len(payload),
		SHA256:      got,
		Command:     handoff.Command,
		VMName:      handoff.VM.Name,
		VMDir:       handoff.VM.Dir,
		BookmarkKey: bookmark.Key,
		BookmarkLen: len(bookmark.Bytes),
		Message:     "decoded handoff and read descriptor passed over Unix socket",
	}, nil
}

func runWorkerStatusPreflightChild(status appleAppSandboxStatus, handoff runWorkerHandoff) (runWorkerChildReport, error) {
	bookmark, ok := handoff.bookmark("vm")
	if !ok {
		return runWorkerChildReport{}, fmt.Errorf("run-worker status preflight missing VM bookmark")
	}
	if len(bookmark.Bytes) == 0 {
		return runWorkerChildReport{}, fmt.Errorf("run-worker status preflight bookmark %s has no bytes", bookmark.Key)
	}
	resolved, stale, stop, err := resolveSecurityScopedBookmark(bookmark.Bytes)
	if err != nil {
		return runWorkerChildReport{}, err
	}
	defer stop()
	if !vmconfig.Validate(resolved) {
		return runWorkerChildReport{}, fmt.Errorf("run-worker status preflight bookmark resolved to invalid VM: %s", resolved)
	}
	configRead := false
	if _, err := os.Stat(filepath.Join(resolved, "config.json")); err == nil {
		if _, err := vmconfig.Load(resolved); err != nil {
			return runWorkerChildReport{}, err
		}
		configRead = true
	} else if !os.IsNotExist(err) {
		return runWorkerChildReport{}, fmt.Errorf("stat vm config: %w", err)
	}
	runtimeRead := false
	if _, err := os.Stat(filepath.Join(resolved, vmRuntimeStateFile)); err == nil {
		if _, err := readVMRuntimeState(resolved); err != nil {
			return runWorkerChildReport{}, err
		}
		runtimeRead = true
	} else if !os.IsNotExist(err) {
		return runWorkerChildReport{}, fmt.Errorf("stat vm runtime: %w", err)
	}
	home, _ := os.UserHomeDir()
	return runWorkerChildReport{
		Action:      "child",
		AppSandbox:  status.Active,
		ContainerID: status.ContainerID,
		HomeDir:     home,
		Command:     handoff.Command,
		VMName:      handoff.VM.Name,
		VMDir:       handoff.VM.Dir,
		ResolvedDir: resolved,
		OSType:      vmconfig.DetectOSType(resolved),
		State:       detectVMState(resolved),
		ConfigRead:  configRead,
		RuntimeRead: runtimeRead,
		Stale:       stale,
		BookmarkKey: bookmark.Key,
		BookmarkLen: len(bookmark.Bytes),
		Message:     "resolved VM bookmark and read metadata",
	}, nil
}

func sendRunWorkerHandoff(ln *net.UnixListener, handoff runWorkerHandoff, files []*os.File, timeout time.Duration) error {
	if err := ln.SetDeadline(time.Now().Add(timeout)); err != nil {
		return fmt.Errorf("set run-worker socket deadline: %w", err)
	}
	conn, err := ln.AcceptUnix()
	if err != nil {
		return fmt.Errorf("accept run-worker socket: %w", err)
	}
	defer conn.Close()
	data, err := encodeRunWorkerHandoff(handoff)
	if err != nil {
		return err
	}
	if len(files) != len(handoff.FDs) {
		return fmt.Errorf("run-worker handoff has %d fd mappings for %d descriptors", len(handoff.FDs), len(files))
	}
	for _, fd := range handoff.FDs {
		if fd.Index >= len(files) {
			return fmt.Errorf("run-worker handoff fd %s index %d, have %d descriptors", fd.Name, fd.Index, len(files))
		}
	}
	var rights []byte
	if len(files) > 0 {
		rightsFDs := make([]int, len(files))
		for i, file := range files {
			rightsFDs[i] = int(file.Fd())
		}
		rights = unix.UnixRights(rightsFDs...)
	}
	if _, _, err := conn.WriteMsgUnix(data, rights, nil); err != nil {
		return fmt.Errorf("send handoff to run-worker: %w", err)
	}
	return nil
}

func receiveRunWorkerHandoff(sockPath string, timeout time.Duration) (runWorkerHandoff, []*os.File, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: sockPath, Net: "unix"})
		if err != nil {
			lastErr = err
			time.Sleep(50 * time.Millisecond)
			continue
		}
		defer conn.Close()
		if err := conn.SetDeadline(deadline); err != nil {
			return runWorkerHandoff{}, nil, fmt.Errorf("set run-worker child deadline: %w", err)
		}
		buf := make([]byte, 64*1024)
		oob := make([]byte, unix.CmsgSpace(4*16))
		n, oobn, flags, _, err := conn.ReadMsgUnix(buf, oob)
		if err != nil {
			return runWorkerHandoff{}, nil, fmt.Errorf("receive handoff from run-worker socket: %w", err)
		}
		if flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 {
			return runWorkerHandoff{}, nil, fmt.Errorf("run-worker handoff was truncated")
		}
		handoff, err := decodeRunWorkerHandoff(buf[:n])
		if err != nil {
			return runWorkerHandoff{}, nil, err
		}
		messages, err := unix.ParseSocketControlMessage(oob[:oobn])
		if err != nil {
			return runWorkerHandoff{}, nil, fmt.Errorf("parse descriptor control message: %w", err)
		}
		var files []*os.File
		for _, message := range messages {
			fds, err := unix.ParseUnixRights(&message)
			if err != nil {
				continue
			}
			for _, fd := range fds {
				files = append(files, os.NewFile(uintptr(fd), "cove-run-worker-grant"))
			}
		}
		if len(files) != len(handoff.FDs) {
			closeRunWorkerFiles(files)
			return runWorkerHandoff{}, nil, fmt.Errorf("run-worker handoff received %d descriptors, want %d", len(files), len(handoff.FDs))
		}
		return handoff, files, nil
	}
	return runWorkerHandoff{}, nil, fmt.Errorf("connect run-worker socket: %w", lastErr)
}

func closeRunWorkerFiles(files []*os.File) {
	for _, file := range files {
		file.Close()
	}
}

func runWorkerChildEnv(env []string) []string {
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if strings.HasPrefix(entry, "COVE_APP_SANDBOX_MACGO=") {
			continue
		}
		out = append(out, entry)
	}
	out = append(out, "COVE_APP_SANDBOX_MACGO=1")
	return out
}

func runWorkerContainerTempDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	return filepath.Join(home, "Library", "Containers", "com.tmc.cove", "Data", "tmp"), nil
}

func firstJSONObjectBytes(out []byte) ([]byte, error) {
	start := bytes.IndexByte(out, '{')
	end := bytes.LastIndexByte(out, '}')
	if start < 0 || end < start {
		return nil, fmt.Errorf("no JSON object found")
	}
	return out[start : end+1], nil
}
