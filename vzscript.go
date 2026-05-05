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
//	guest-terminal <file>       Run a script in the guest's terminal application (macOS Terminal.app, Linux GNOME Terminal/Konsole/xterm)
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
//	wait-menu-text <text> [timeout]  Wait for menu bar text
//	reboot-to-recovery [timeout] Stop VM and start macOS Recovery
//	recovery-options [timeout] Select Options in the Recovery startup picker
//	startup-options [timeout]  Alias for recovery-options
//	recovery-continue [timeout] Continue from Recovery setup screens
//	label-push <text>          Push a script label onto the VM window title
//	label-pop                  Pop the current script label
//	label-clear                Clear all script labels
//	answer-visible [-optional] [-skip-empty] [-timeout duration] [-delay duration] [-progress text] <prompt> <answer>...
//	type <text>                 Type text into the VM
//	type-keycodes <text>        Type text using per-key keycode events
//	key <spec>                  Send key event (e.g. "return", "tab", "cmd+v")
//	click <x> <y>              Click at normalized coordinates (0-1)
//	wait-prompt-clear <text> [timeout]  Wait until a prompt clears or progresses
//	detect-page                 Detect Setup Assistant page via OCR
//	detect-screen               Detect screen state (desktop/login/setup)
//
// Conditions:
//
//	[screen:desktop]                    True if current screen matches state
//	[page:language]                     True if current SA page matches name
//	[text-visible:Continue]             True if text is visible on screen
//	[text-visible:Authorized+user]      Space and punctuation use URL encoding
//	[text-visible:%5By%2Fn%5D]          Encoded form of "[y/n]"
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
	"image"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	ocrx "github.com/tmc/apple/x/vzkit/ocr"
	"google.golang.org/protobuf/proto"
	"rsc.io/script"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

// vzscriptConfig holds configuration for the vzscript engine.
type vzscriptConfig struct {
	socketPath   string
	guestOS      string
	execTimeout  time.Duration
	verbose      bool
	terminal     bool // force guest-shell/guest-exec to run in the guest terminal
	autoApprove  bool // auto-click "Allow"/"OK" on system dialogs via OCR
	daemon       bool // use daemon agent (root) instead of user agent
	logWriter    io.Writer
	streamOut    io.Writer
	streamErr    io.Writer
	env          []string // extra environment variables (KEY=VALUE)
	hostLogFile  *os.File // persistent log file in VM directory
	controlSrv   *ControlServer
	labels       *vzscriptLabelStack
	template     bool
	templateVars map[string]any
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
	if cfg.labels == nil {
		cfg.labels = &vzscriptLabelStack{}
	}
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
		"ocr-click":          vzOCRClickCmd(cfg),
		"ocr-wait":           vzOCRWaitCmd(cfg),
		"ocr-gone":           vzOCRGoneCmd(cfg),
		"ocr":                vzOCRCmd(cfg),
		"screenshot":         vzScreenshotCmd(cfg),
		"wait-menu-text":     vzWaitMenuTextCmd(cfg),
		"click-menu-item":    vzClickMenuItemCmd(cfg),
		"reboot-to-recovery": vzRebootToRecoveryCmd(cfg),
		"recovery-options":   vzRecoveryOptionsCmd(cfg),
		"startup-options":    vzRecoveryOptionsCmd(cfg),
		"recovery-continue":  vzRecoveryContinueCmd(cfg),
		"label-push":         vzLabelPushCmd(cfg),
		"label-pop":          vzLabelPopCmd(cfg),
		"label-clear":        vzLabelClearCmd(cfg),
		"answer-visible":     vzAnswerVisibleCmd(cfg),
		"type":               vzTypeCmd(cfg),
		"type-keycodes":      vzTypeKeycodesCmd(cfg),
		"key":                vzKeyCmd(cfg),
		"click":              vzClickCmd(cfg),
		"wait-prompt-clear":  vzWaitPromptClearCmd(cfg),
		"wait":               vzWaitCmd(),
		"detect-page":        vzDetectPageCmd(cfg),
		"detect-screen":      vzDetectScreenCmd(cfg),

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

// guestWaitCmd waits for the VM to be ready for real work: the daemon agent
// must be reachable, AND provisioning must have completed (i.e. the user
// exists and has been added to admin). On a fresh boot the agent comes up
// well before the LaunchDaemon's sysadminctl finishes; without this second
// gate, vzscripts that look up the admin user race past the user-create
// step and fail with "no admin user found".
//
// Provisioning-complete is signalled by /var/db/.vz-provisioned. If that
// marker never appears (the VM wasn't provisioned by cove), fall back to
// agent-ping after a short grace period so non-provisioned VMs still work.
//
// Usage: guest-wait [timeout]
// Default timeout is 10m. Polls quickly after the daemon agent appears so
// first-boot provisioning can hand off as soon as the marker is written.
func guestWaitCmd(cfg vzscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "wait for VM boot, agent reachable, and provisioning complete",
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
			agentReadyAt := time.Time{}
			// If the marker never appears (existing VM, not cove-provisioned),
			// fall through after this grace period past agent-ready.
			const provisionGrace = 30 * time.Second
			const agentPoll = 2 * time.Second
			const provisionPoll = time.Second
			for time.Now().Before(deadline) {
				attempt++
				if attempt == 1 {
					s.Logf("waiting for guest agent and provisioning (timeout %s)...\n", timeout)
				}

				resp, err := ctlSendRequest(cfg.socketPath,
					&controlpb.ControlRequest{Type: "agent-ping"},
					3*time.Second, "agent-ping")
				if err != nil || !resp.Success {
					time.Sleep(agentPoll)
					continue
				}
				if agentReadyAt.IsZero() {
					agentReadyAt = time.Now()
				}

				// Agent is up; now check for the provisioning marker via
				// daemon-exec (root daemon answers regardless of login state).
				done, derr := provisionMarkerPresent(cfg)
				if derr == nil && done {
					return func(*script.State) (string, string, error) {
						return fmt.Sprintf("agent ready and provisioned after %d attempt(s)\n", attempt), "", nil
					}, nil
				}
				if time.Since(agentReadyAt) > provisionGrace {
					return func(*script.State) (string, string, error) {
						return fmt.Sprintf("agent ready after %d attempt(s) (no .vz-provisioned marker after %s; assuming non-provisioned VM)\n", attempt, provisionGrace), "", nil
					}, nil
				}
				time.Sleep(provisionPoll)
			}
			return nil, fmt.Errorf("timeout after %s waiting for guest agent and provisioning", timeout)
		},
	)
}

// provisionMarkerPresent checks for /var/db/.vz-provisioned via the daemon
// agent (which runs as root and is available before any user logs in).
// Returns (true, nil) if the marker exists, (false, nil) if it does not,
// or (false, err) if the check itself failed.
func provisionMarkerPresent(cfg vzscriptConfig) (bool, error) {
	req := &controlpb.ControlRequest{
		Type: "agent-exec",
		Command: &controlpb.ControlRequest_AgentExec{
			AgentExec: &controlpb.AgentExecCommand{
				Args: []string{"/bin/test", "-f", "/var/db/.vz-provisioned"},
			},
		},
	}
	resp, err := ctlSendRequest(cfg.socketPath, req, 10*time.Second, "agent-exec")
	if err != nil {
		return false, err
	}
	if !resp.Success {
		return false, fmt.Errorf("%s", resp.Error)
	}
	var result struct {
		ExitCode int `json:"exitCode"`
	}
	if err := json.Unmarshal([]byte(resp.Data), &result); err != nil {
		return false, err
	}
	return result.ExitCode == 0, nil
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
			Summary: "run a script in the guest's terminal application (macOS Terminal.app, Linux GNOME Terminal/Konsole/xterm)",
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

	// Always use streaming so long-running commands (guest-shell scripts,
	// package installs) don't appear to hang. In verbose mode, output is
	// printed live; otherwise it is collected silently.
	var onStdout, onStderr func([]byte)
	if cfg.verbose {
		out := cfg.streamOut
		if out == nil {
			out = os.Stdout
		}
		errOut := cfg.streamErr
		if errOut == nil {
			errOut = os.Stderr
		}
		onStdout = func(chunk []byte) { _, _ = out.Write(chunk) }
		onStderr = func(chunk []byte) { _, _ = errOut.Write(chunk) }
	}
	return func(*script.State) (string, string, error) {
		stdout, stderr, exitCode, err := guestExecStream(
			cfg,
			args,
			timeout,
			onStdout,
			onStderr,
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

// guestExecInTerminal runs a script in the guest GUI terminal so the user can watch.
func guestExecInTerminal(cfg vzscriptConfig, guestPath string) (script.WaitFunc, error) {
	client := NewControlClient(cfg.socketPath)
	osName, err := detectGuestOS(client)
	if err != nil {
		return nil, err
	}
	if osName == guestOSLinux {
		return func(*script.State) (string, string, error) {
			err := launchLinuxGuestTerminal(client, "", []string{"/bin/bash", guestPath})
			if err != nil {
				return "", "", err
			}
			return "opened guest terminal\n", "", nil
		}, nil
	}
	if osName != guestOSDarwin {
		return nil, fmt.Errorf("guest terminal launch is not implemented for %s", osName)
	}

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
			hostPath := s.Path(expandTilde(args[0]))
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
			hostPath := s.Path(expandTilde(args[0]))
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
			if _, regionErr := ocrx.ParseSearchOptions(region); regionErr != nil {
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
	if _, parseErr := ocrx.ParseSearchOptions(region); parseErr != nil {
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
			if exec := newScriptBootExecutor(cfg); exec != nil {
				switch region {
				case "", "screen":
					return nil, exec.clickText(text, d)
				case "menu":
					return nil, exec.hostClickTextWithOptions(text, d, ocrx.MenuSearchOptions())
				}
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
			if exec := newScriptBootExecutor(cfg); exec != nil {
				switch region {
				case "", "screen":
					return nil, exec.waitForText(text, d)
				case "menu":
					return nil, exec.waitForTextWithOptions(text, d, ocrx.MenuSearchOptions())
				}
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
			if exec := newScriptBootExecutor(cfg); exec != nil && (region == "" || region == "screen" || region == "menu") {
				deadline := time.Now().Add(d)
				opts := ocrx.SearchOptions{}
				if region == "menu" {
					opts = ocrx.MenuSearchOptions()
				}
				for time.Now().Before(deadline) {
					img := exec.captureScreen()
					if img == nil {
						time.Sleep(500 * time.Millisecond)
						continue
					}
					_, _, found := exec.ocr.FindTextWithOptions(img, text, opts)
					if !found {
						return nil, nil
					}
					time.Sleep(500 * time.Millisecond)
				}
				return nil, fmt.Errorf("timeout waiting for text %q to disappear", text)
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

func newScriptBootExecutor(cfg vzscriptConfig) *automationExecutor {
	if cfg.controlSrv == nil {
		return nil
	}
	debugDir := ""
	if debugOCR {
		debugDir = filepath.Join(vmDir, "debug")
	}
	return newAutomationExecutor(ocrx.NewService(cfg.verbose), cfg.controlSrv, cfg.verbose, debugDir)
}

func vzWaitMenuTextCmd(cfg vzscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "wait for menu-bar text to appear via OCR",
			Args:    "text [timeout]",
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			if len(args) == 0 {
				return nil, script.ErrUsage
			}
			text := args[0]
			timeout := "60s"
			if len(args) > 1 {
				timeout = args[1]
			}
			d, err := time.ParseDuration(timeout)
			if err != nil {
				return nil, fmt.Errorf("invalid timeout %q: %w", timeout, err)
			}
			if exec := newScriptBootExecutor(cfg); exec != nil {
				return nil, exec.waitForTextWithOptions(text, d, ocrx.MenuSearchOptions())
			}
			resp, err := ctlSendOCRWithRegion(cfg.socketPath, "ocr-wait", text, timeout, "menu", d+10*time.Second)
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

func vzClickMenuItemCmd(cfg vzscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "click a menu title and then a menu item",
			Args:    "menu item [timeout]",
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			if len(args) < 2 || len(args) > 3 {
				return nil, script.ErrUsage
			}
			menu := args[0]
			item := args[1]
			timeout := 60 * time.Second
			if len(args) == 3 {
				d, err := time.ParseDuration(args[2])
				if err != nil {
					return nil, fmt.Errorf("invalid timeout %q: %w", args[2], err)
				}
				timeout = d
			}
			if exec := newScriptBootExecutor(cfg); exec != nil {
				prevCapture := cfg.controlSrv.captureBackend()
				prevInput := cfg.controlSrv.inputBackend()
				cfg.controlSrv.setCaptureBackend(automationBackendWindow)
				cfg.controlSrv.setInputBackend(automationBackendWindow)
				defer func() {
					cfg.controlSrv.setCaptureBackend(prevCapture)
					cfg.controlSrv.setInputBackend(prevInput)
				}()
				return nil, exec.clickMenuItem(menu, item, timeout)
			}
			client := NewControlClient(cfg.socketPath)
			client.SetTimeout(timeout)
			_ = client.SetGUICaptureBackend("window")
			_ = client.SetGUIInputBackend("window")
			defer func() {
				_ = client.SetGUICaptureBackend("auto")
				_ = client.SetGUIInputBackend("auto")
			}()
			ocr := ocrx.NewService(cfg.verbose)
			return nil, clickMenuItemViaClient(client, ocr, menu, item, timeout)
		},
	)
}

func vzRebootToRecoveryCmd(cfg vzscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "stop the VM and start macOS Recovery",
			Args:    "[timeout]",
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			timeout := 2 * time.Minute
			if len(args) > 1 {
				return nil, script.ErrUsage
			}
			if len(args) == 1 {
				d, err := time.ParseDuration(args[0])
				if err != nil {
					return nil, fmt.Errorf("invalid timeout %q: %w", args[0], err)
				}
				timeout = d
			}
			if cfg.controlSrv != nil {
				done := make(chan *controlpb.ControlResponse, 1)
				go func() {
					done <- cfg.controlSrv.rebootToRecovery()
				}()
				select {
				case resp := <-done:
					if !resp.Success {
						return nil, fmt.Errorf("%s", resp.Error)
					}
					return nil, nil
				case <-time.After(timeout):
					return nil, fmt.Errorf("reboot to recovery timed out")
				}
			}
			req := &controlpb.ControlRequest{Type: "reboot-to-recovery"}
			resp, err := ctlSendRequest(cfg.socketPath, req, timeout, "reboot-to-recovery")
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

func vzRecoveryOptionsCmd(cfg vzscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "select Options in the Apple Silicon Recovery startup picker",
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			if len(args) > 1 {
				return nil, script.ErrUsage
			}
			timeout := 60 * time.Second
			if len(args) == 1 {
				d, err := time.ParseDuration(args[0])
				if err != nil {
					return nil, fmt.Errorf("invalid timeout %q: %w", args[0], err)
				}
				timeout = d
			}
			if exec := newScriptBootExecutor(cfg); exec != nil {
				return nil, exec.activateStartupOptions(timeout)
			}
			client := NewControlClient(cfg.socketPath)
			client.SetTimeout(timeout)
			ocr := ocrx.NewService(cfg.verbose)
			return nil, activateStartupOptionsViaClient(client, ocr, timeout)
		},
	)
}

func vzRecoveryContinueCmd(cfg vzscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "advance the Recovery language/Continue screen if it is present",
			Args:    "[timeout]",
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			timeout := 60 * time.Second
			if len(args) > 1 {
				return nil, script.ErrUsage
			}
			if len(args) == 1 {
				d, err := time.ParseDuration(args[0])
				if err != nil {
					return nil, fmt.Errorf("invalid timeout %q: %w", args[0], err)
				}
				timeout = d
			}
			if exec := newScriptBootExecutor(cfg); exec != nil {
				return nil, exec.continueRecoveryLanguage(timeout)
			}
			client := NewControlClient(cfg.socketPath)
			client.SetTimeout(timeout)
			ocr := ocrx.NewService(cfg.verbose)
			return nil, continueRecoveryLanguageViaClient(client, ocr, timeout)
		},
	)
}

func recoveryAuthFailedOCR(ocr *ocrx.Service, img image.Image) bool {
	return pageContainsAnyOCR(ocr, img,
		"Authentication failure",
		"Failed to authenticate",
		"failed to set credential",
	)
}

type vzscriptLabelStack struct {
	mu     sync.Mutex
	labels []string
}

func (l *vzscriptLabelStack) push(label string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	label = cleanVZScriptLabel(label)
	if label != "" {
		l.labels = append(l.labels, label)
	}
	return strings.Join(l.labels, " / ")
}

func (l *vzscriptLabelStack) pop() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.labels) > 0 {
		l.labels = l.labels[:len(l.labels)-1]
	}
	return strings.Join(l.labels, " / ")
}

func (l *vzscriptLabelStack) clear() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.labels = nil
	return ""
}

func cleanVZScriptLabel(label string) string {
	label = strings.TrimSpace(label)
	if len(label) < 2 {
		return label
	}
	quote := label[0]
	if quote != '\'' && quote != '"' {
		return label
	}
	if label[len(label)-1] != quote {
		return label
	}
	return strings.TrimSpace(label[1 : len(label)-1])
}

func vzLabelPushCmd(cfg vzscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "push a label onto the VM window title",
			Args:    "text",
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			if len(args) == 0 {
				return nil, script.ErrUsage
			}
			label := strings.Join(args, " ")
			current := cfg.labels.push(label)
			s.Logf("label-push %q -> %q\n", cleanVZScriptLabel(label), current)
			setVZScriptWindowLabel(cfg, current, s)
			return nil, nil
		},
	)
}

func vzLabelPopCmd(cfg vzscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{Summary: "pop the current VM window title label"},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			if len(args) != 0 {
				return nil, script.ErrUsage
			}
			current := cfg.labels.pop()
			s.Logf("label-pop -> %q\n", current)
			setVZScriptWindowLabel(cfg, current, s)
			return nil, nil
		},
	)
}

func vzLabelClearCmd(cfg vzscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{Summary: "clear VM window title labels"},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			if len(args) != 0 {
				return nil, script.ErrUsage
			}
			cfg.labels.clear()
			s.Logf("label-clear\n")
			setVZScriptWindowLabel(cfg, "", s)
			return nil, nil
		},
	)
}

func setVZScriptWindowLabel(cfg vzscriptConfig, label string, s *script.State) {
	if cfg.controlSrv != nil {
		cfg.controlSrv.SetWindowTitleLabel(label)
		return
	}
	if cfg.socketPath == "" {
		return
	}
	req := &controlpb.ControlRequest{
		Type: "window-label",
		Command: &controlpb.ControlRequest_Text{
			Text: &controlpb.TextCommand{Text: label},
		},
	}
	resp, err := ctlSendRequest(cfg.socketPath, req, 5*time.Second, "window-label")
	if err != nil {
		if s != nil {
			s.Logf("label window update skipped: %v\n", err)
		}
		return
	}
	if !resp.Success {
		if s != nil {
			s.Logf("label window update skipped: %s\n", resp.Error)
		}
	}
}

type answerVisibleArgs struct {
	timeout   time.Duration
	delay     time.Duration
	optional  bool
	skipEmpty bool
	progress  []string
	pairs     []promptAnswer
}

type promptAnswer struct {
	prompt string
	answer string
}

func vzAnswerVisibleCmd(cfg vzscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "answer the first visible prompt from a set of alternatives",
			Args:    "[-optional] [-skip-empty] [-timeout duration] [-delay duration] [-progress text] <prompt> <answer>...",
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			opts, err := parseAnswerVisibleArgs(args)
			if err != nil {
				return nil, err
			}
			if cfg.verbose {
				s.Logf("answer-visible (%d prompts, timeout %s)\n", len(opts.pairs), opts.timeout)
			}

			if exec := newScriptBootExecutor(cfg); exec != nil {
				err := runAnswerVisible(
					s,
					exec.ocr,
					exec.captureScreen,
					exec.typeTextKeycodes,
					exec.sendKey,
					opts,
				)
				return nil, err
			}

			client := NewControlClient(cfg.socketPath)
			client.SetTimeout(60 * time.Second)
			ocr := ocrx.NewService(cfg.verbose)
			err = runAnswerVisible(
				s,
				ocr,
				func() image.Image {
					img, err := client.Screenshot()
					if err != nil {
						return nil
					}
					return img
				},
				func(text string) error {
					return typeTextKeycodesViaClient(client, text)
				},
				func(spec string) error {
					return sendKeySpecViaClient(client, spec)
				},
				opts,
			)
			return nil, err
		},
	)
}

func parseAnswerVisibleArgs(args []string) (answerVisibleArgs, error) {
	opts := answerVisibleArgs{timeout: 30 * time.Second, delay: 1500 * time.Millisecond}
	i := 0
	for i < len(args) {
		switch args[i] {
		case "-optional":
			opts.optional = true
			i++
		case "-skip-empty":
			opts.skipEmpty = true
			i++
		case "-timeout":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("-timeout requires a value")
			}
			d, err := time.ParseDuration(args[i])
			if err != nil {
				return opts, fmt.Errorf("invalid timeout %q: %w", args[i], err)
			}
			opts.timeout = d
			i++
		case "-delay":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("-delay requires a value")
			}
			d, err := time.ParseDuration(args[i])
			if err != nil {
				return opts, fmt.Errorf("invalid delay %q: %w", args[i], err)
			}
			opts.delay = d
			i++
		case "-progress":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("-progress requires a value")
			}
			opts.progress = append(opts.progress, args[i])
			i++
		default:
			goto pairs
		}
	}
pairs:
	rest := args[i:]
	if len(rest) == 0 || len(rest)%2 != 0 {
		return opts, script.ErrUsage
	}
	for i := 0; i < len(rest); i += 2 {
		if opts.skipEmpty && rest[i+1] == "" {
			continue
		}
		opts.pairs = append(opts.pairs, promptAnswer{prompt: rest[i], answer: rest[i+1]})
	}
	if len(opts.pairs) == 0 && opts.optional {
		return opts, nil
	}
	if len(opts.pairs) == 0 {
		return opts, script.ErrUsage
	}
	return opts, nil
}

func runAnswerVisible(s *script.State, ocr *ocrx.Service, capture func() image.Image, typeKeycodes func(string) error, key func(string) error, opts answerVisibleArgs) error {
	if ocr == nil {
		return fmt.Errorf("answer-visible: missing ocr service")
	}
	if len(opts.pairs) == 0 && opts.optional {
		return nil
	}
	deadline := time.Now().Add(opts.timeout)
	for time.Now().Before(deadline) {
		img := capture()
		if img == nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		if pair, ok := visiblePrompt(ocr, img, opts.pairs); ok {
			if s != nil {
				s.Logf("answer-visible matched %q\n", pair.prompt)
			}
			if err := answerVisiblePrompt(typeKeycodes, key, pair.answer, opts.delay); err != nil {
				return err
			}
			return waitAnswerProgress(ocr, capture, pair.prompt, opts.progress, 20*time.Second)
		}
		time.Sleep(500 * time.Millisecond)
	}
	if opts.optional {
		return nil
	}
	return fmt.Errorf("timeout waiting for visible prompt")
}

func visiblePrompt(ocr *ocrx.Service, img image.Image, pairs []promptAnswer) (promptAnswer, bool) {
	text := normalizeVisibleText(ocr.AllText(img))
	for _, pair := range pairs {
		if strings.Contains(text, normalizeVisibleText(pair.prompt)) {
			return pair, true
		}
	}
	return promptAnswer{}, false
}

func answerVisiblePrompt(typeKeycodes func(string) error, key func(string) error, text string, delay time.Duration) error {
	if delay > 0 {
		time.Sleep(delay)
	}
	if err := typeKeycodes(text); err != nil {
		return fmt.Errorf("answer prompt: %w", err)
	}
	if err := key("return"); err != nil {
		return fmt.Errorf("submit prompt: %w", err)
	}
	return nil
}

func waitAnswerProgress(ocr *ocrx.Service, capture func() image.Image, prompt string, progress []string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		img := capture()
		if img == nil {
			time.Sleep(250 * time.Millisecond)
			continue
		}
		if promptClearedOCR(ocr, img, prompt) || pageContainsAnyOCR(ocr, img, progress...) {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for prompt %q to progress", prompt)
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

func vzTypeKeycodesCmd(cfg vzscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "type text using per-key keycode events",
			Args:    "text",
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			if len(args) == 0 {
				return nil, script.ErrUsage
			}
			text := strings.Join(args, " ")
			if cfg.verbose {
				s.Logf("type-keycodes %q\n", text)
			}
			client := NewControlClient(cfg.socketPath)
			client.SetTimeout(60 * time.Second)
			if err := typeTextKeycodesViaClient(client, text); err != nil {
				return nil, err
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

func vzWaitPromptClearCmd(cfg vzscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "wait until a prompt clears or the screen advances",
			Args:    "text [timeout]",
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			if len(args) < 1 || len(args) > 2 {
				return nil, script.ErrUsage
			}
			needle := args[0]
			timeout := 5 * time.Second
			if len(args) == 2 {
				d, err := time.ParseDuration(args[1])
				if err != nil {
					return nil, fmt.Errorf("invalid timeout %q: %w", args[1], err)
				}
				timeout = d
			}
			if exec := newScriptBootExecutor(cfg); exec != nil {
				deadline := time.Now().Add(timeout)
				for time.Now().Before(deadline) {
					img := exec.captureScreen()
					if img == nil {
						time.Sleep(250 * time.Millisecond)
						continue
					}
					if strings.Contains(strings.ToLower(needle), "password") && exec.recoveryAuthFailed(img) {
						return nil, fmt.Errorf("%s", recoveryAuthFailureMessage(needle))
					}
					if promptClearedOCR(exec.ocr, img, needle) {
						return nil, nil
					}
					time.Sleep(250 * time.Millisecond)
				}
				return nil, fmt.Errorf("timeout waiting for prompt %q to clear", needle)
			}

			client := NewControlClient(cfg.socketPath)
			client.SetTimeout(timeout + 10*time.Second)
			ocr := ocrx.NewService(cfg.verbose)
			deadline := time.Now().Add(timeout)
			lowerNeedle := strings.ToLower(needle)
			for time.Now().Before(deadline) {
				img, err := client.Screenshot()
				if err != nil {
					time.Sleep(250 * time.Millisecond)
					continue
				}
				if strings.Contains(lowerNeedle, "password") && recoveryAuthFailedOCR(ocr, img) {
					return nil, fmt.Errorf("%s", recoveryAuthFailureMessage(needle))
				}
				if promptClearedOCR(ocr, img, needle) {
					return nil, nil
				}
				time.Sleep(250 * time.Millisecond)
			}
			return nil, fmt.Errorf("timeout waiting for prompt %q to clear", needle)
		},
	)
}

func typeTextKeycodesViaClient(client *ControlClient, text string) error {
	for _, ch := range text {
		info, ok := charToKeyCode[ch]
		if !ok {
			return fmt.Errorf("no keycode for %q", ch)
		}
		var modifiers uint32
		if info.shift {
			modifiers = uint32(ModifierShift)
		}
		for _, down := range []bool{true, false} {
			req := &controlpb.ControlRequest{
				Type: "key",
				Command: &controlpb.ControlRequest_Key{
					Key: &controlpb.KeyCommand{
						KeyCode:    uint32(info.keyCode),
						KeyDown:    down,
						Modifiers:  modifiers,
						Character:  string(ch),
						UseCgEvent: false,
					},
				},
			}
			resp, err := client.sendRequest(req)
			if err != nil {
				return err
			}
			if !resp.Success {
				if down {
					return fmt.Errorf("type key down %q: %s", ch, resp.Error)
				}
				return fmt.Errorf("type key up %q: %s", ch, resp.Error)
			}
			time.Sleep(30 * time.Millisecond)
		}
	}
	return nil
}

func sendKeySpecViaClient(client *ControlClient, spec string) error {
	if !isValidKeySpec(spec) {
		return fmt.Errorf("invalid key spec %q", spec)
	}
	keyCode, modifiers := parseKeySpec(spec)
	if err := client.sendKeyEvent(uint16(keyCode), true, uint(modifiers), true); err != nil {
		return err
	}
	time.Sleep(50 * time.Millisecond)
	return client.sendKeyEvent(uint16(keyCode), false, uint(modifiers), true)
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
			needle, err := url.QueryUnescape(suffix)
			if err != nil {
				return false, fmt.Errorf("decode text-visible suffix %q: %w", suffix, err)
			}
			resp, err := ctlSendOCR(cfg.socketPath, "ocr-all-text", "", "", 30*time.Second)
			if err != nil {
				return false, nil
			}
			if !resp.Success {
				return false, nil
			}
			return strings.Contains(normalizeVisibleText(resp.Data), normalizeVisibleText(needle)), nil
		},
	)
}

func normalizeVisibleText(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}
