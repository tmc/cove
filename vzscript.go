// vzscript.go - Script engine for VM provisioning.
//
// Extends rsc.io/script with commands for interacting with a guest VM
// via the control socket and guest agent, plus OCR-driven GUI automation.
// Scripts are standard txtar archives executed by rsc.io/script.
//
// Guest commands:
//
//	guest-wait [timeout]        Wait for VM boot and agent connectivity
//	guest-ping                  Check agent connectivity
//	guest-exec <args...>        Run a command in the guest
//	guest-shell <file>          Run a local script file in the guest via bash
//	guest-terminal <file>       Run a local script file in Terminal.app (visible to user)
//	guest-write <dst> <src>     Copy a local file to the guest
//	guest-read <path>           Read a guest file to stdout
//	guest-cp <host> <guest>     Copy a file or directory host→guest (streaming)
//	host-cp <host> <guest>      Copy a host file/directory to guest (long timeout)
//	append-path <dir>           Add a directory to system PATH via /etc/paths.d/
//
// UI automation commands (via control socket):
//
//	ocr-click <text> [timeout] [region]  Find text via OCR and click it
//	ocr-wait <text> [timeout] [region]   Wait until text appears
//	ocr-gone <text> [timeout] [region]   Wait until text disappears
//	ocr                         Run OCR on current screen; stdout is all text
//	screenshot [file]           Capture VM screen to file
//	type <text>                 Type text into the VM
//	key <spec>                  Send key event (e.g. "return", "tab", "cmd+v")
//	click <x> <y>              Click at normalized coordinates (0-1)
//	detect-page                 Detect Setup Assistant page via OCR
//	detect-screen               Detect screen state (desktop/login/setup)
//
// Conditions:
//
//	[screen:<state>]            True if current screen matches state
//	[page:<name>]               True if current SA page matches name
//	[text-visible:<text>]       True if text is visible on screen
//
// Example:
//
//	# Phase 1: OCR-driven GUI automation
//	ocr-wait Continue 120s
//	ocr-click Continue
//	wait 2s
//
//	# Phase 2: Guest agent commands
//	guest-wait 3m
//	guest-ping
//	guest-shell install.sh
//
//	-- install.sh --
//	#!/bin/bash
//	xcode-select --install
package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	"rsc.io/script"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

// vzscriptConfig holds configuration for the vzscript engine.
type vzscriptConfig struct {
	socketPath  string
	execTimeout time.Duration
	verbose     bool
	terminal    bool // force guest-shell/guest-exec to run in Terminal.app
	autoApprove bool // auto-click "Allow"/"OK" on system dialogs via OCR
	daemon      bool // use daemon agent (root) instead of user agent
	logWriter   io.Writer
	streamOut   io.Writer
	streamErr   io.Writer
}

// execType returns the control request type for exec commands,
// routing to daemon or user agent based on config.
func (c vzscriptConfig) execType() string {
	if c.daemon {
		return "agent-exec"
	}
	return "agent-user-exec"
}

// execStreamType returns the control request type for streaming exec commands.
func (c vzscriptConfig) execStreamType() string {
	if c.daemon {
		return "agent-exec-stream"
	}
	return "agent-user-exec-stream"
}

// newVZScriptEngine returns a script engine with guest VM commands and
// UI automation commands. Both command sets communicate over the control socket.
func newVZScriptEngine(cfg vzscriptConfig) *script.Engine {
	defaults := script.DefaultCmds()
	cmds := map[string]script.Cmd{
		// Guest commands.
		"guest-wait":     guestWaitCmd(cfg),
		"guest-ping":     guestPingCmd(cfg),
		"guest-exec":     guestExecCmd(cfg),
		"guest-shell":    guestShellCmd(cfg),
		"guest-terminal": guestTerminalCmd(cfg),
		"guest-write":    guestWriteCmd(cfg),
		"guest-read":     guestReadCmd(cfg),
		"guest-cp":       guestCpCmd(cfg),
		"host-cp":        hostCpCmd(cfg),
		"append-path":    appendPathCmd(cfg),

		// UI automation commands (via control socket).
		"ocr-click":     vzOCRClickCmd(cfg),
		"ocr-wait":      vzOCRWaitCmd(cfg),
		"ocr-gone":      vzOCRGoneCmd(cfg),
		"ocr":           vzOCRCmd(cfg),
		"screenshot":    vzScreenshotCmd(cfg),
		"type":          vzTypeCmd(cfg),
		"key":           vzKeyCmd(cfg),
		"click":         vzClickCmd(cfg),
		"wait":          vzWaitCmd(),
		"detect-page":   vzDetectPageCmd(cfg),
		"detect-screen": vzDetectScreenCmd(cfg),

		// Standard commands.
		"cat":    defaults["cat"],
		"cp":     defaults["cp"],
		"echo":   defaults["echo"],
		"env":    defaults["env"],
		"exists": defaults["exists"],
		"help":   defaults["help"],
		"mkdir":  defaults["mkdir"],
		"sleep":  defaults["sleep"],
		"stderr": defaults["stderr"],
		"stdout": defaults["stdout"],
		"stop":   defaults["stop"],
	}

	conds := script.DefaultConds()
	conds["screen"] = vzScreenCond(cfg)
	conds["page"] = vzPageCond(cfg)
	conds["text-visible"] = vzTextVisibleCond(cfg)

	return &script.Engine{
		Cmds:  cmds,
		Conds: conds,
	}
}

// guestWaitCmd waits for the VM control socket and guest agent to be reachable.
// Usage: guest-wait [timeout]
// Default timeout is 10m. Polls every 5s until the agent responds to a ping.
func guestWaitCmd(cfg vzscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "wait for VM to boot and guest agent to be reachable",
			Args:    "[timeout]",
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			timeout := 10 * time.Minute
			if len(args) > 0 {
				d, err := time.ParseDuration(args[0])
				if err != nil {
					return nil, fmt.Errorf("invalid timeout %q: %w", args[0], err)
				}
				timeout = d
			}

			deadline := time.Now().Add(timeout)
			attempt := 0
			for time.Now().Before(deadline) {
				attempt++
				resp, err := ctlSendRequest(cfg.socketPath,
					&controlpb.ControlRequest{Type: "agent-ping"},
					10*time.Second, "agent-ping")
				if err == nil && resp.Success {
					return func(*script.State) (string, string, error) {
						return fmt.Sprintf("agent ready after %d attempt(s)\n", attempt), "", nil
					}, nil
				}
				if attempt == 1 {
					s.Logf("waiting for guest agent (timeout %s)...\n", timeout)
				}
				time.Sleep(5 * time.Second)
			}
			return nil, fmt.Errorf("timeout after %s waiting for guest agent", timeout)
		},
	)
}

func guestPingCmd(cfg vzscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{Summary: "check guest agent connectivity"},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			resp, err := ctlSendRequest(cfg.socketPath,
				&controlpb.ControlRequest{Type: "agent-ping"},
				10*time.Second, "agent-ping")
			if err != nil {
				return nil, err
			}
			if !resp.Success {
				return nil, fmt.Errorf("%s", resp.Error)
			}
			return func(*script.State) (string, string, error) {
				return resp.Data + "\n", "", nil
			}, nil
		},
	)
}

func guestExecCmd(cfg vzscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "run a command in the guest VM",
			Args:    "command [args...]",
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			if len(args) == 0 {
				return nil, script.ErrUsage
			}
			return guestExec(cfg, args)
		},
	)
}

func guestShellCmd(cfg vzscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "copy a script to the guest and run it with bash",
			Args:    "script-file",
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			if len(args) != 1 {
				return nil, script.ErrUsage
			}
			data, err := os.ReadFile(s.Path(args[0]))
			if err != nil {
				return nil, err
			}
			// Write script to guest temp location.
			guestPath := "/tmp/vzscript-" + args[0]
			if err := guestWriteFile(cfg.socketPath, guestPath, data, 0755); err != nil {
				return nil, err
			}
			if cfg.terminal {
				return guestExecInTerminal(cfg, guestPath)
			}
			return guestExec(cfg, []string{"/bin/bash", guestPath})
		},
	)
}

func guestTerminalCmd(cfg vzscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "run a script in Terminal.app inside the guest (visible to user)",
			Args:    "script-file",
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			if len(args) != 1 {
				return nil, script.ErrUsage
			}
			data, err := os.ReadFile(s.Path(args[0]))
			if err != nil {
				return nil, err
			}
			// Write script to guest temp location.
			guestPath := "/tmp/vzscript-" + args[0]
			if err := guestWriteFile(cfg.socketPath, guestPath, data, 0755); err != nil {
				return nil, err
			}
			return guestExecInTerminal(cfg, guestPath)
		},
	)
}

func guestWriteCmd(cfg vzscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "copy a local file to the guest VM",
			Args:    "guest-path local-path",
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			if len(args) != 2 {
				return nil, script.ErrUsage
			}
			data, err := os.ReadFile(s.Path(args[1]))
			if err != nil {
				return nil, err
			}
			return nil, guestWriteFile(cfg.socketPath, args[0], data, 0644)
		},
	)
}

func guestReadCmd(cfg vzscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "read a file from the guest VM to stdout",
			Args:    "guest-path",
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			if len(args) != 1 {
				return nil, script.ErrUsage
			}
			req := &controlpb.ControlRequest{
				Type: "agent-read",
				Command: &controlpb.ControlRequest_AgentRead{
					AgentRead: &controlpb.AgentFileReadCommand{
						Path: args[0],
					},
				},
			}
			resp, err := ctlSendRequest(cfg.socketPath, req, 30*time.Second, "agent-read")
			if err != nil {
				return nil, err
			}
			if !resp.Success {
				return nil, fmt.Errorf("%s", resp.Error)
			}
			data, err := base64.StdEncoding.DecodeString(resp.Data)
			if err != nil {
				return nil, fmt.Errorf("decode: %w", err)
			}
			return func(*script.State) (string, string, error) {
				return string(data), "", nil
			}, nil
		},
	)
}

// guestExec runs a command in the guest and returns a WaitFunc.
func guestExec(cfg vzscriptConfig, args []string) (script.WaitFunc, error) {
	timeout := cfg.execTimeout
	if timeout == 0 {
		timeout = 10 * time.Minute
	}

	// In verbose mode, stream command output live so long-running installs
	// show progress as they run.
	if cfg.verbose {
		out := cfg.streamOut
		if out == nil {
			out = os.Stdout
		}
		errOut := cfg.streamErr
		if errOut == nil {
			errOut = os.Stderr
		}
		return func(*script.State) (string, string, error) {
			stdout, stderr, exitCode, err := guestExecStream(
				cfg,
				args,
				timeout,
				func(chunk []byte) { _, _ = out.Write(chunk) },
				func(chunk []byte) { _, _ = errOut.Write(chunk) },
			)
			if err != nil {
				return "", "", err
			}
			var execErr error
			if exitCode != 0 {
				execErr = fmt.Errorf("exit code %d", exitCode)
			}
			return stdout, stderr, execErr
		}, nil
	}

	req := &controlpb.ControlRequest{
		Type: cfg.execType(),
		Command: &controlpb.ControlRequest_AgentExec{
			AgentExec: &controlpb.AgentExecCommand{
				Args: args,
			},
		},
	}
	resp, err := ctlSendRequest(cfg.socketPath, req, timeout, cfg.execType())
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	var result struct {
		ExitCode int32  `json:"exitCode"`
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
	}
	if err := json.Unmarshal([]byte(resp.Data), &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return func(*script.State) (string, string, error) {
		var execErr error
		if result.ExitCode != 0 {
			execErr = fmt.Errorf("exit code %d", result.ExitCode)
		}
		return result.Stdout, result.Stderr, execErr
	}, nil
}

// guestExecStream runs agent-exec-stream and returns collected stdout/stderr
// plus final exit code. Optional callbacks receive live chunk data.
func guestExecStream(cfg vzscriptConfig, args []string, timeout time.Duration, onStdout, onStderr func([]byte)) (string, string, int32, error) {
	req := &controlpb.ControlRequest{
		Type: cfg.execStreamType(),
		Command: &controlpb.ControlRequest_AgentExec{
			AgentExec: &controlpb.AgentExecCommand{Args: args},
		},
	}

	conn, err := net.DialTimeout("unix", cfg.socketPath, timeout)
	if err != nil {
		return "", "", 0, ctlConnectError(cfg.socketPath, err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(timeout))

	reqToSend := req
	if req.AuthToken == "" {
		if token := resolveControlTokenForSocket(cfg.socketPath); token != "" {
			reqToSend = proto.Clone(req).(*controlpb.ControlRequest)
			reqToSend.AuthToken = token
		}
	}
	reqBytes, err := protojsonMarshaler.Marshal(reqToSend)
	if err != nil {
		return "", "", 0, fmt.Errorf("marshal: %w", err)
	}
	reqBytes = append(reqBytes, '\n')
	if _, err := conn.Write(reqBytes); err != nil {
		return "", "", 0, fmt.Errorf("send: %w", err)
	}

	reader := bufio.NewReaderSize(conn, 256*1024)
	var stdoutBuf, stderrBuf bytes.Buffer
	var exitCode int32
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return "", "", 0, fmt.Errorf("receive: %w", err)
		}

		var resp controlpb.ControlResponse
		if err := protojsonUnmarshaler.Unmarshal([]byte(line), &resp); err != nil {
			return "", "", 0, fmt.Errorf("parse response: %w", err)
		}
		if !resp.Success {
			return stdoutBuf.String(), stderrBuf.String(), exitCode, fmt.Errorf("%s", resp.Error)
		}
		if resp.Data == "" {
			continue
		}

		var event struct {
			Stream   string `json:"stream"`
			Data     string `json:"data"`
			Done     bool   `json:"done"`
			ExitCode int32  `json:"exitCode"`
		}
		if err := json.Unmarshal([]byte(resp.Data), &event); err != nil {
			// Fallback: treat non-event payload as stdout text.
			stdoutBuf.WriteString(resp.Data)
			if onStdout != nil {
				onStdout([]byte(resp.Data))
			}
			continue
		}

		if event.Data != "" {
			chunk, err := base64.StdEncoding.DecodeString(event.Data)
			if err != nil {
				return "", "", 0, fmt.Errorf("decode stream chunk: %w", err)
			}
			if event.Stream == "stderr" {
				stdoutOrStderrWrite(&stderrBuf, onStderr, chunk)
			} else {
				stdoutOrStderrWrite(&stdoutBuf, onStdout, chunk)
			}
		}

		if event.Done {
			exitCode = event.ExitCode
			break
		}
	}

	return stdoutBuf.String(), stderrBuf.String(), exitCode, nil
}

func stdoutOrStderrWrite(buf *bytes.Buffer, sink func([]byte), chunk []byte) {
	_, _ = buf.Write(chunk)
	if sink != nil {
		sink(chunk)
	}
}

// guestExecInTerminal runs a script in Terminal.app so the user can watch.
// It launches Terminal with "open -a Terminal <wrapper>" as the console user.
// This avoids AppleEvents automation prompts from osascript while still
// executing the target script with root privileges via sudo.
//
// To avoid sudo password prompts, a temporary NOPASSWD entry is created
// in /etc/sudoers.d/ before the script runs.
func guestExecInTerminal(cfg vzscriptConfig, guestPath string) (script.WaitFunc, error) {
	user, err := guestConsoleUser(cfg)
	if err != nil {
		// No console user — run directly as root (won't open Terminal).
		return guestExec(cfg, []string{"/bin/bash", guestPath})
	}

	// Grant temporary passwordless sudo for all commands.
	// The sudoers entry is removed after the script finishes.
	sudoersFile := "/etc/sudoers.d/vzscript-terminal"
	sudoersLine := fmt.Sprintf("%s ALL=(ALL) NOPASSWD: ALL\n", user)
	if err := guestWriteFile(cfg.socketPath, sudoersFile, []byte(sudoersLine), 0440); err != nil {
		fmt.Fprintf(os.Stderr, "[guest-terminal] warning: could not write sudoers: %v\n", err)
	}

	// Build a wrapper launched by Terminal. It runs the target script via sudo
	// and then removes the temporary sudoers entry.
	wrapperPath := guestPath + ".command"
	wrapper := fmt.Sprintf(`#!/bin/bash
set +e
sudo /bin/bash %s
status=$?
sudo rm -f %s
echo
echo "[vzscript] finished with exit code $status"
exit $status
`, shellEscape(guestPath), shellEscape(sudoersFile))
	if err := guestWriteFile(cfg.socketPath, wrapperPath, []byte(wrapper), 0755); err != nil {
		return nil, fmt.Errorf("write terminal wrapper: %w", err)
	}

	openCmd := "open -a Terminal " + shellEscape(wrapperPath)
	return guestExec(cfg, []string{"su", "-l", user, "-c", openCmd})
}

// guestConsoleUser returns the username of the currently logged-in console user.
func guestConsoleUser(cfg vzscriptConfig) (string, error) {
	req := &controlpb.ControlRequest{
		Type: "agent-exec",
		Command: &controlpb.ControlRequest_AgentExec{
			AgentExec: &controlpb.AgentExecCommand{
				Args: []string{"/usr/bin/stat", "-f", "%Su", "/dev/console"},
			},
		},
	}
	resp, err := ctlSendRequest(cfg.socketPath, req, 10*time.Second, "agent-exec")
	if err != nil {
		return "", err
	}
	if !resp.Success {
		return "", fmt.Errorf("%s", resp.Error)
	}
	var result struct {
		ExitCode int    `json:"exitCode"`
		Stdout   string `json:"stdout"`
	}
	if err := json.Unmarshal([]byte(resp.Data), &result); err != nil {
		return "", err
	}
	user := strings.TrimSpace(result.Stdout)
	if user == "" || user == "root" {
		return "", fmt.Errorf("no console user logged in")
	}
	return user, nil
}

// guestWriteFile writes data to a file in the guest.
func guestWriteFile(socketPath, guestPath string, data []byte, mode uint32) error {
	req := &controlpb.ControlRequest{
		Type: "agent-write",
		Command: &controlpb.ControlRequest_AgentWrite{
			AgentWrite: &controlpb.AgentFileWriteCommand{
				Path: guestPath,
				Data: base64.StdEncoding.EncodeToString(data),
				Mode: mode,
			},
		},
	}
	resp, err := ctlSendRequest(socketPath, req, 30*time.Second, "agent-write")
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("%s", resp.Error)
	}
	return nil
}

func guestCpCmd(cfg vzscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "copy a file between host and guest (streaming)",
			Args:    "[-from-guest] host-path guest-path",
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			fromGuest := false
			if len(args) > 0 && args[0] == "-from-guest" {
				fromGuest = true
				args = args[1:]
			}
			if len(args) != 2 {
				return nil, script.ErrUsage
			}
			hostPath := s.Path(args[0])
			guestPath := args[1]

			req := &controlpb.ControlRequest{
				Type: "agent-cp",
				Command: &controlpb.ControlRequest_AgentCp{
					AgentCp: &controlpb.AgentCopyCommand{
						HostPath:  hostPath,
						GuestPath: guestPath,
						ToGuest:   !fromGuest,
					},
				},
			}
			timeout := cfg.execTimeout
			if timeout == 0 {
				timeout = 10 * time.Minute
			}
			resp, err := ctlSendRequest(cfg.socketPath, req, timeout, "agent-cp")
			if err != nil {
				return nil, err
			}
			if !resp.Success {
				return nil, fmt.Errorf("%s", resp.Error)
			}
			return func(*script.State) (string, string, error) {
				return resp.Data + "\n", "", nil
			}, nil
		},
	)
}

// hostCpCmd copies a host file or directory to the guest.
// Unlike guest-cp, this always copies host→guest and uses a longer default
// timeout suitable for large directory copies (e.g., Xcode.app).
func hostCpCmd(cfg vzscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "copy a host file or directory to the guest (streaming)",
			Args:    "[-force] [-timeout duration] host-path guest-path",
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			timeout := 30 * time.Minute
			force := false
			for len(args) >= 1 {
				switch args[0] {
				case "-force":
					force = true
					args = args[1:]
					continue
				case "-timeout":
					if len(args) < 2 {
						return nil, fmt.Errorf("-timeout requires a duration argument")
					}
					d, err := time.ParseDuration(args[1])
					if err != nil {
						return nil, fmt.Errorf("invalid timeout: %w", err)
					}
					timeout = d
					args = args[2:]
					continue
				}
				break
			}
			if len(args) != 2 {
				return nil, script.ErrUsage
			}
			hostPath := args[0]
			guestPath := args[1]

			if info, err := os.Stat(hostPath); err == nil {
				var size string
				if info.IsDir() {
					size = "directory"
				} else {
					size = fmt.Sprintf("%.1f MB", float64(info.Size())/(1024*1024))
				}
				fmt.Fprintf(os.Stderr, "[host-cp] %s -> guest:%s (%s)\n", hostPath, guestPath, size)
			}

			req := &controlpb.ControlRequest{
				Type: "agent-cp",
				Command: &controlpb.ControlRequest_AgentCp{
					AgentCp: &controlpb.AgentCopyCommand{
						HostPath:  hostPath,
						GuestPath: guestPath,
						ToGuest:   true,
						Overwrite: force,
					},
				},
			}
			resp, err := ctlSendRequest(cfg.socketPath, req, timeout, "agent-cp")
			if err != nil {
				return nil, err
			}
			if !resp.Success {
				return nil, fmt.Errorf("%s", resp.Error)
			}
			return func(*script.State) (string, string, error) {
				return resp.Data + "\n", "", nil
			}, nil
		},
	)
}

// appendPathCmd adds a directory to the system PATH via /etc/paths.d/.
// Usage: append-path /usr/local/go/bin
//
// The file name is derived from the path: for /usr/local/go/bin, the parent
// directory name "go" is used, creating /etc/paths.d/go. If the last component
// is unique enough (not "bin", "sbin", etc.), it is used directly.
//
// The command is idempotent: it checks whether the path already exists in any
// file under /etc/paths.d/ before writing.
func appendPathCmd(cfg vzscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "add a directory to the system PATH via /etc/paths.d/",
			Args:    "path",
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			if len(args) != 1 {
				return nil, script.ErrUsage
			}
			dir := filepath.Clean(args[0])
			name := pathsDName(dir)

			// Shell script: check if path already exists in /etc/paths.d/,
			// and write it if not.
			script := fmt.Sprintf(`#!/bin/bash
set -eu
target=%s
name=%s
if grep -rqxF "$target" /etc/paths.d/ 2>/dev/null; then
  echo "already in /etc/paths.d/"
  exit 0
fi
echo "$target" > "/etc/paths.d/$name"
echo "wrote /etc/paths.d/$name"
`, shellEscape(dir), shellEscape(name))

			if err := guestWriteFile(cfg.socketPath, "/tmp/vzscript-append-path.sh", []byte(script), 0755); err != nil {
				return nil, fmt.Errorf("write append-path script: %w", err)
			}
			return guestExec(cfg, []string{"/bin/bash", "/tmp/vzscript-append-path.sh"})
		},
	)
}

// pathsDName returns a suitable filename for /etc/paths.d/ given a directory path.
// For paths ending in generic names like "bin" or "sbin", the parent directory
// name is used instead (e.g., /usr/local/go/bin -> "go", /opt/homebrew/bin -> "homebrew").
func pathsDName(dir string) string {
	generic := map[string]bool{
		"bin": true, "sbin": true, "libexec": true,
	}
	base := filepath.Base(dir)
	if generic[base] {
		parent := filepath.Base(filepath.Dir(dir))
		if parent != "." && parent != "/" {
			return parent
		}
	}
	return base
}

// --- UI automation commands (via control socket) ---

// ctlSendOCR sends an OCR command with optional text/timeout params.
func ctlSendOCR(sock, cmdType, text, timeout string, readTimeout time.Duration) (*controlpb.ControlResponse, error) {
	return ctlSendOCRWithRegion(sock, cmdType, text, timeout, "", readTimeout)
}

// ctlSendOCRWithRegion sends an OCR command with optional text/timeout/region params.
func ctlSendOCRWithRegion(sock, cmdType, text, timeout, region string, readTimeout time.Duration) (*controlpb.ControlResponse, error) {
	obj := map[string]interface{}{
		"type": cmdType,
	}
	data := map[string]string{}
	if text != "" {
		data["text"] = text
	}
	if timeout != "" {
		data["timeout"] = timeout
	}
	if region != "" {
		data["region"] = region
	}
	if len(data) > 0 {
		obj["data"] = data
	}
	return ctlSendJSON(sock, obj, readTimeout)
}

func parseOCROptionalTimeoutRegion(args []string, defaultTimeout string) (text, timeout, region string, err error) {
	if len(args) == 0 {
		return "", "", "", script.ErrUsage
	}
	if len(args) > 3 {
		return "", "", "", script.ErrUsage
	}

	text = args[0]
	timeout = defaultTimeout
	if len(args) == 1 {
		return text, timeout, "", nil
	}

	if _, parseErr := time.ParseDuration(args[1]); parseErr == nil {
		timeout = args[1]
		if len(args) == 3 {
			region = args[2]
			if _, regionErr := ParseOCRSearchOptions(region); regionErr != nil {
				return "", "", "", regionErr
			}
		}
		return text, timeout, region, nil
	}

	region = args[1]
	if len(args) == 3 {
		timeout = args[2]
		if _, parseErr := time.ParseDuration(timeout); parseErr != nil {
			return "", "", "", fmt.Errorf("invalid timeout %q", timeout)
		}
	}
	if _, parseErr := ParseOCRSearchOptions(region); parseErr != nil {
		return "", "", "", parseErr
	}
	return text, timeout, region, nil
}

func vzOCRClickCmd(cfg vzscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "find text on screen via OCR and click its center",
			Args:    "text [timeout] [region]",
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			text, timeout, region, err := parseOCROptionalTimeoutRegion(args, "10s")
			if err != nil {
				return nil, err
			}
			if cfg.verbose {
				s.Logf("ocr-click %q (timeout %s, region %q)\n", text, timeout, region)
			}
			d, _ := time.ParseDuration(timeout)
			if d == 0 {
				d = 30 * time.Second
			}
			resp, err := ctlSendOCRWithRegion(cfg.socketPath, "ocr-click", text, timeout, region, d+10*time.Second)
			if err != nil {
				return nil, err
			}
			if !resp.Success {
				return nil, fmt.Errorf("%s", resp.Error)
			}
			return func(*script.State) (string, string, error) {
				return resp.Data + "\n", "", nil
			}, nil
		},
	)
}

func vzOCRWaitCmd(cfg vzscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "wait for text to appear on screen via OCR",
			Args:    "text [timeout] [region]",
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			text, timeout, region, err := parseOCROptionalTimeoutRegion(args, "60s")
			if err != nil {
				return nil, err
			}
			if cfg.verbose {
				s.Logf("ocr-wait %q (timeout %s, region %q)\n", text, timeout, region)
			}
			d, _ := time.ParseDuration(timeout)
			if d == 0 {
				d = 120 * time.Second
			}
			resp, err := ctlSendOCRWithRegion(cfg.socketPath, "ocr-wait", text, timeout, region, d+10*time.Second)
			if err != nil {
				return nil, err
			}
			if !resp.Success {
				return nil, fmt.Errorf("%s", resp.Error)
			}
			return func(*script.State) (string, string, error) {
				return resp.Data + "\n", "", nil
			}, nil
		},
	)
}

func vzOCRGoneCmd(cfg vzscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "wait for text to disappear from screen via OCR",
			Args:    "text [timeout] [region]",
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			text, timeout, region, err := parseOCROptionalTimeoutRegion(args, "30s")
			if err != nil {
				return nil, err
			}
			d, _ := time.ParseDuration(timeout)
			if d == 0 {
				d = 60 * time.Second
			}
			resp, err := ctlSendOCRWithRegion(cfg.socketPath, "ocr-gone", text, timeout, region, d+10*time.Second)
			if err != nil {
				return nil, err
			}
			if !resp.Success {
				return nil, fmt.Errorf("%s", resp.Error)
			}
			return func(*script.State) (string, string, error) {
				return resp.Data + "\n", "", nil
			}, nil
		},
	)
}

func vzOCRCmd(cfg vzscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{Summary: "run OCR on current screen; stdout is all recognized text"},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			resp, err := ctlSendOCR(cfg.socketPath, "ocr-all-text", "", "", 30*time.Second)
			if err != nil {
				return nil, err
			}
			if !resp.Success {
				return nil, fmt.Errorf("%s", resp.Error)
			}
			return func(*script.State) (string, string, error) {
				return resp.Data + "\n", "", nil
			}, nil
		},
	)
}

func vzScreenshotCmd(cfg vzscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "capture VM screen to JPEG file",
			Args:    "[file]",
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			req := &controlpb.ControlRequest{
				Type: "screenshot",
				Command: &controlpb.ControlRequest_Screenshot{
					Screenshot: &controlpb.ScreenshotCommand{
						Format: "jpeg",
					},
				},
			}
			resp, err := ctlSendRequest(cfg.socketPath, req, 30*time.Second, "screenshot")
			if err != nil {
				return nil, err
			}
			if !resp.Success {
				return nil, fmt.Errorf("%s", resp.Error)
			}
			if len(args) > 0 {
				imgData, err := base64.StdEncoding.DecodeString(resp.Data)
				if err != nil {
					return nil, fmt.Errorf("decode screenshot: %w", err)
				}
				path := s.Path(args[0])
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					return nil, fmt.Errorf("mkdir: %w", err)
				}
				if err := os.WriteFile(path, imgData, 0644); err != nil {
					return nil, err
				}
				return func(*script.State) (string, string, error) {
					return path + "\n", "", nil
				}, nil
			}
			return func(*script.State) (string, string, error) {
				return "screenshot captured\n", "", nil
			}, nil
		},
	)
}

func vzTypeCmd(cfg vzscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "type text into the VM",
			Args:    "text",
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			if len(args) == 0 {
				return nil, script.ErrUsage
			}
			text := strings.Join(args, " ")
			if cfg.verbose {
				s.Logf("type %q\n", text)
			}
			req := &controlpb.ControlRequest{
				Type: "text",
				Command: &controlpb.ControlRequest_Text{
					Text: &controlpb.TextCommand{
						Text: text,
					},
				},
			}
			resp, err := ctlSendRequest(cfg.socketPath, req, 60*time.Second, "text")
			if err != nil {
				return nil, err
			}
			if !resp.Success {
				return nil, fmt.Errorf("%s", resp.Error)
			}
			return nil, nil
		},
	)
}

func vzKeyCmd(cfg vzscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "send a key event to the VM",
			Args:    "keyspec",
			Detail:  []string{"Examples: return, tab, escape, space, cmd+v, shift+a, cmd+shift+3"},
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			if len(args) == 0 {
				return nil, script.ErrUsage
			}
			spec := args[0]
			if !isValidKeySpec(spec) {
				return nil, fmt.Errorf("invalid key spec %q", spec)
			}
			keyCode, modifiers := parseKeySpec(spec)
			if cfg.verbose {
				s.Logf("key %s (code=%d mods=%d)\n", spec, keyCode, modifiers)
			}
			// Key down.
			req := &controlpb.ControlRequest{
				Type: "key",
				Command: &controlpb.ControlRequest_Key{
					Key: &controlpb.KeyCommand{
						KeyCode:    uint32(keyCode),
						KeyDown:    true,
						Modifiers:  uint32(modifiers),
						UseCgEvent: true,
					},
				},
			}
			resp, err := ctlSendRequest(cfg.socketPath, req, 10*time.Second, "key")
			if err != nil {
				return nil, err
			}
			if !resp.Success {
				return nil, fmt.Errorf("key down: %s", resp.Error)
			}
			time.Sleep(50 * time.Millisecond)
			// Key up.
			req.Command = &controlpb.ControlRequest_Key{
				Key: &controlpb.KeyCommand{
					KeyCode:    uint32(keyCode),
					KeyDown:    false,
					Modifiers:  uint32(modifiers),
					UseCgEvent: true,
				},
			}
			resp, err = ctlSendRequest(cfg.socketPath, req, 10*time.Second, "key")
			if err != nil {
				return nil, err
			}
			if !resp.Success {
				return nil, fmt.Errorf("key up: %s", resp.Error)
			}
			return nil, nil
		},
	)
}

func vzClickCmd(cfg vzscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "click at normalized coordinates (0-1, top-left origin)",
			Args:    "x y",
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			if len(args) != 2 {
				return nil, script.ErrUsage
			}
			var x, y float64
			if _, err := fmt.Sscanf(args[0], "%f", &x); err != nil {
				return nil, fmt.Errorf("invalid x %q: %w", args[0], err)
			}
			if _, err := fmt.Sscanf(args[1], "%f", &y); err != nil {
				return nil, fmt.Errorf("invalid y %q: %w", args[1], err)
			}
			if cfg.verbose {
				s.Logf("click (%.3f, %.3f)\n", x, y)
			}
			req := &controlpb.ControlRequest{
				Type: "mouse",
				Command: &controlpb.ControlRequest_Mouse{
					Mouse: &controlpb.MouseCommand{
						X: x, Y: y, Button: 0, Action: "click",
					},
				},
			}
			resp, err := ctlSendRequest(cfg.socketPath, req, 10*time.Second, "mouse")
			if err != nil {
				return nil, err
			}
			if !resp.Success {
				return nil, fmt.Errorf("click: %s", resp.Error)
			}
			return nil, nil
		},
	)
}

func vzWaitCmd() script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "sleep for a duration",
			Args:    "duration",
			Detail:  []string{"Examples: 1s, 500ms, 2m"},
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			if len(args) == 0 {
				return nil, script.ErrUsage
			}
			d, err := time.ParseDuration(args[0])
			if err != nil {
				return nil, fmt.Errorf("invalid duration %q: %w", args[0], err)
			}
			time.Sleep(d)
			return nil, nil
		},
	)
}

func vzDetectPageCmd(cfg vzscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{Summary: "detect current Setup Assistant page via OCR"},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			resp, err := ctlSendOCR(cfg.socketPath, "detect-page", "", "", 30*time.Second)
			if err != nil {
				return nil, err
			}
			if !resp.Success {
				return nil, fmt.Errorf("%s", resp.Error)
			}
			return func(*script.State) (string, string, error) {
				return resp.Data + "\n", "", nil
			}, nil
		},
	)
}

func vzDetectScreenCmd(cfg vzscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{Summary: "detect screen state (desktop, login, setup_assistant, etc.)"},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			resp, err := ctlSendOCR(cfg.socketPath, "detect-screen", "", "", 30*time.Second)
			if err != nil {
				return nil, err
			}
			if !resp.Success {
				return nil, fmt.Errorf("%s", resp.Error)
			}
			return func(*script.State) (string, string, error) {
				return resp.Data + "\n", "", nil
			}, nil
		},
	)
}

// --- Conditions ---

func vzScreenCond(cfg vzscriptConfig) script.Cond {
	return script.PrefixCondition(
		"current screen state matches",
		func(s *script.State, suffix string) (bool, error) {
			resp, err := ctlSendOCR(cfg.socketPath, "detect-screen", "", "", 30*time.Second)
			if err != nil {
				return false, nil
			}
			if !resp.Success {
				return false, nil
			}
			return resp.Data == suffix, nil
		},
	)
}

func vzPageCond(cfg vzscriptConfig) script.Cond {
	return script.PrefixCondition(
		"current Setup Assistant page matches",
		func(s *script.State, suffix string) (bool, error) {
			resp, err := ctlSendOCR(cfg.socketPath, "detect-page", "", "", 30*time.Second)
			if err != nil {
				return false, nil
			}
			if !resp.Success {
				return false, nil
			}
			return resp.Data == suffix, nil
		},
	)
}

func vzTextVisibleCond(cfg vzscriptConfig) script.Cond {
	return script.PrefixCondition(
		"text is visible on screen",
		func(s *script.State, suffix string) (bool, error) {
			resp, err := ctlSendOCR(cfg.socketPath, "ocr-all-text", "", "", 30*time.Second)
			if err != nil {
				return false, nil
			}
			if !resp.Success {
				return false, nil
			}
			return strings.Contains(strings.ToLower(resp.Data), strings.ToLower(suffix)), nil
		},
	)
}
