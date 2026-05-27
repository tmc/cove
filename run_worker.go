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

	"golang.org/x/sys/unix"
)

const runWorkerFDMessage = "cove run-worker fd\n"

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
	Bytes       int    `json:"bytes"`
	SHA256      string `json:"sha256"`
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

	sockPath := filepath.Join(dir, "rw.sock")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: sockPath, Net: "unix"})
	if err != nil {
		return runWorkerProbeReport{}, fmt.Errorf("listen run-worker socket: %w", err)
	}
	defer ln.Close()

	sendErr := make(chan error, 1)
	go func() {
		sendErr <- sendRunWorkerFD(ln, file, 45*time.Second)
	}()

	exe, err := os.Executable()
	if err != nil {
		return runWorkerProbeReport{}, fmt.Errorf("find executable: %w", err)
	}
	cmd := exec.Command(exe, "__run-worker", "child", "-sock", sockPath, "-want-sha256", want)
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
	if !child.ReceivedFD || child.SHA256 != want {
		return runWorkerProbeReport{}, fmt.Errorf("sandboxed worker descriptor proof failed: %+v", child)
	}
	return runWorkerProbeReport{
		Action:           "probe",
		ParentAppSandbox: appleAppSandboxActive(),
		Child:            child,
		Message:          "sandboxed worker received and read explicit descriptor",
	}, nil
}

func runWorkerChildCommand(env commandEnv, args []string) error {
	fs := flag.NewFlagSet("__run-worker child", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	sockPath := fs.String("sock", "", "Unix socket for descriptor handoff")
	wantSHA := fs.String("want-sha256", "", "expected descriptor payload SHA256")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 || *sockPath == "" || *wantSHA == "" {
		return fmt.Errorf("usage: cove __run-worker child -sock path -want-sha256 hex")
	}

	report, err := runWorkerChild(*sockPath, *wantSHA)
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

func runWorkerChild(sockPath, wantSHA string) (runWorkerChildReport, error) {
	status := currentAppleAppSandboxStatus()
	if !status.Active {
		return runWorkerChildReport{}, fmt.Errorf("run-worker child requires Apple App Sandbox")
	}
	file, err := receiveRunWorkerFD(sockPath, 45*time.Second)
	if err != nil {
		return runWorkerChildReport{}, err
	}
	defer file.Close()

	payload, err := io.ReadAll(file)
	if err != nil {
		return runWorkerChildReport{}, fmt.Errorf("read descriptor grant: %w", err)
	}
	sum := sha256.Sum256(payload)
	got := hex.EncodeToString(sum[:])
	if got != wantSHA {
		return runWorkerChildReport{}, fmt.Errorf("descriptor grant sha256 = %s, want %s", got, wantSHA)
	}
	home, _ := os.UserHomeDir()
	return runWorkerChildReport{
		Action:      "child",
		AppSandbox:  status.Active,
		ContainerID: status.ContainerID,
		HomeDir:     home,
		ReceivedFD:  true,
		Bytes:       len(payload),
		SHA256:      got,
		Message:     "read descriptor passed over Unix socket",
	}, nil
}

func sendRunWorkerFD(ln *net.UnixListener, file *os.File, timeout time.Duration) error {
	if err := ln.SetDeadline(time.Now().Add(timeout)); err != nil {
		return fmt.Errorf("set run-worker socket deadline: %w", err)
	}
	conn, err := ln.AcceptUnix()
	if err != nil {
		return fmt.Errorf("accept run-worker socket: %w", err)
	}
	defer conn.Close()
	rights := unix.UnixRights(int(file.Fd()))
	if _, _, err := conn.WriteMsgUnix([]byte(runWorkerFDMessage), rights, nil); err != nil {
		return fmt.Errorf("send descriptor to run-worker: %w", err)
	}
	return nil
}

func receiveRunWorkerFD(sockPath string, timeout time.Duration) (*os.File, error) {
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
			return nil, fmt.Errorf("set run-worker child deadline: %w", err)
		}
		buf := make([]byte, len(runWorkerFDMessage))
		oob := make([]byte, unix.CmsgSpace(4))
		_, oobn, _, _, err := conn.ReadMsgUnix(buf, oob)
		if err != nil {
			return nil, fmt.Errorf("receive descriptor from run-worker socket: %w", err)
		}
		messages, err := unix.ParseSocketControlMessage(oob[:oobn])
		if err != nil {
			return nil, fmt.Errorf("parse descriptor control message: %w", err)
		}
		for _, message := range messages {
			fds, err := unix.ParseUnixRights(&message)
			if err != nil {
				continue
			}
			if len(fds) == 0 {
				continue
			}
			return os.NewFile(uintptr(fds[0]), "cove-run-worker-grant"), nil
		}
		return nil, fmt.Errorf("run-worker socket did not include descriptor")
	}
	return nil, fmt.Errorf("connect run-worker socket: %w", lastErr)
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
