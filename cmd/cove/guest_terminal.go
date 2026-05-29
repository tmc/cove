package main

import (
	"fmt"
	"strings"
	"time"
)

const (
	guestOSDarwin = "darwin"
	guestOSLinux  = "linux"
)

var linuxTerminalPrograms = []string{"gnome-terminal", "kgx", "konsole", "xterm"}

func detectGuestOS(client guestTerminalAgent) (string, error) {
	resp, err := client.AgentExecTypedTimeout([]string{"uname", "-s"}, nil, "", 10*time.Second)
	if err != nil {
		return "", fmt.Errorf("detect guest os: %w", err)
	}
	if resp.GetExitCode() != 0 {
		return "", fmt.Errorf("detect guest os: %s", strings.TrimSpace(resp.GetStderr()))
	}
	switch strings.ToLower(strings.TrimSpace(resp.GetStdout())) {
	case "darwin":
		return guestOSDarwin, nil
	case "linux":
		return guestOSLinux, nil
	default:
		return "", fmt.Errorf("unsupported guest os %q", strings.TrimSpace(resp.GetStdout()))
	}
}

func launchGuestTerminal(client guestTerminalAgent, user string, command []string) error {
	if len(command) == 0 {
		return fmt.Errorf("terminal command is required")
	}
	osName, err := detectGuestOS(client)
	if err != nil {
		return err
	}
	switch osName {
	case guestOSLinux:
		return launchLinuxGuestTerminal(client, user, command)
	default:
		return fmt.Errorf("guest terminal launch is not implemented for %s", osName)
	}
}

func launchLinuxGuestTerminal(client guestTerminalAgent, user string, command []string) error {
	if len(command) == 0 {
		return fmt.Errorf("terminal command is required")
	}
	session, err := findLinuxGraphicalSession(client, user)
	if err != nil {
		return err
	}
	program, err := findLinuxTerminalProgram(client)
	if err != nil {
		return err
	}
	args := linuxTerminalLaunchArgs(session, program, command)
	resp, err := client.AgentExecTypedTimeout(args, nil, "", 15*time.Second)
	if err != nil {
		return fmt.Errorf("launch terminal: %w", err)
	}
	if resp.GetExitCode() != 0 {
		return fmt.Errorf("launch terminal: %s", strings.TrimSpace(resp.GetStderr()))
	}
	return nil
}

func findLinuxTerminalProgram(client guestTerminalAgent) (string, error) {
	for _, program := range linuxTerminalPrograms {
		resp, err := client.AgentExecTypedTimeout([]string{"/bin/sh", "-c", "command -v " + shellEscape(program)}, nil, "", 5*time.Second)
		if err == nil && resp.GetExitCode() == 0 && strings.TrimSpace(resp.GetStdout()) != "" {
			return program, nil
		}
	}
	return "", fmt.Errorf("no supported terminal installed in guest (tried: %s)", strings.Join(linuxTerminalPrograms, ", "))
}

func linuxTerminalLaunchArgs(session linuxGraphicalSession, program string, command []string) []string {
	args := []string{"runuser", "-u", session.User, "--", "env"}
	args = append(args, session.Env...)
	args = append(args, program)
	switch program {
	case "gnome-terminal":
		args = append(args, "--window", "--active", "--maximize", "--")
	case "kgx":
		args = append(args, "--")
	default:
		args = append(args, "-e")
	}
	return append(args, "bash", "-lc", shellJoin(command))
}

func shellJoin(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = shellEscape(arg)
	}
	return strings.Join(quoted, " ")
}
