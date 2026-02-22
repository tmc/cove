// vzscript.go - Script engine for VM provisioning.
//
// Extends rsc.io/script with commands for interacting with a guest VM
// via the control socket and guest agent. Scripts are standard txtar
// archives executed by rsc.io/script.
//
// Guest commands:
//
//	guest-ping                  Check agent connectivity
//	guest-exec <args...>        Run a command in the guest
//	guest-shell <file>          Run a local script file in the guest via bash
//	guest-terminal <file>       Run a local script file in Terminal.app (visible to user)
//	guest-write <dst> <src>     Copy a local file to the guest
//	guest-read <path>           Read a guest file to stdout
//
// Example:
//
//	guest-ping
//	guest-write /etc/profile.d/dev.sh dev.sh
//	guest-shell install.sh
//
//	-- dev.sh --
//	export DEVELOPER_DIR=/Library/Developer/CommandLineTools
//
//	-- install.sh --
//	#!/bin/bash
//	xcode-select --install
package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"rsc.io/script"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

// vzscriptConfig holds configuration for the vzscript engine.
type vzscriptConfig struct {
	socketPath  string
	execTimeout time.Duration
	verbose     bool
	terminal    bool // force guest-shell/guest-exec to run in Terminal.app
}

// newVZScriptEngine returns a script engine with guest VM commands.
func newVZScriptEngine(cfg vzscriptConfig) *script.Engine {
	defaults := script.DefaultCmds()
	cmds := map[string]script.Cmd{
		// Guest commands.
		"guest-ping":     guestPingCmd(cfg),
		"guest-exec":     guestExecCmd(cfg),
		"guest-shell":    guestShellCmd(cfg),
		"guest-terminal": guestTerminalCmd(cfg),
		"guest-write":    guestWriteCmd(cfg),
		"guest-read":     guestReadCmd(cfg),
		"guest-cp":       guestCpCmd(cfg),

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
	return &script.Engine{
		Cmds:  cmds,
		Conds: script.DefaultConds(),
	}
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
			// Use osascript to open Terminal.app and run the script.
			// This makes the script visible to the user in the VM GUI.
			osa := fmt.Sprintf(
				`tell application "Terminal" to do script "%s; exit"`,
				strings.ReplaceAll(guestPath, `"`, `\"`),
			)
			return guestExec(cfg, []string{"osascript", "-e", osa})
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
	req := &controlpb.ControlRequest{
		Type: "agent-exec",
		Command: &controlpb.ControlRequest_AgentExec{
			AgentExec: &controlpb.AgentExecCommand{
				Args: args,
			},
		},
	}
	resp, err := ctlSendRequest(cfg.socketPath, req, timeout, "agent-exec")
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

// guestExecInTerminal runs a script in Terminal.app so the user can watch.
func guestExecInTerminal(cfg vzscriptConfig, guestPath string) (script.WaitFunc, error) {
	osa := fmt.Sprintf(
		`tell application "Terminal" to do script "%s; exit"`,
		strings.ReplaceAll(guestPath, `"`, `\"`),
	)
	return guestExec(cfg, []string{"osascript", "-e", osa})
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
