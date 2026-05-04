package main

import (
	"fmt"
	"strings"
	"time"
)

func statusCommand() error {
	client := NewControlClient(GetControlSocketPath())
	osName, err := detectGuestOS(client)
	if err != nil {
		return err
	}

	status := agentHealthState{daemonStatus: "connected", userStatus: "unknown"}
	if agentStatus, err := client.AgentExecTypedTimeout([]string{"true"}, nil, "", 5*time.Second); err != nil || agentStatus.GetExitCode() != 0 {
		status.daemonStatus = "disconnected"
	}
	if status.daemonStatus == "connected" {
		switch osName {
		case guestOSLinux:
			session, ok, err := probeLinuxGUISessionControl(client)
			if err != nil {
				return err
			}
			status.guiSession = session
			status.guiSessionActive = ok
		case guestOSDarwin:
			session, ok, err := probeMacOSGUISessionControl(client)
			if err != nil {
				return err
			}
			status.guiSession = session
			status.guiSessionActive = ok
		}
	}
	fmt.Println(agentHealthSummary(status))
	return nil
}

func probeLinuxGUISessionControl(client *ControlClient) (guiSession, bool, error) {
	resp, err := client.AgentExecTypedTimeout([]string{"loginctl", "list-sessions", "--output", "json", "--no-pager"}, nil, "", 5*time.Second)
	if err != nil {
		return guiSession{}, false, fmt.Errorf("query gui sessions: %w", err)
	}
	if resp.GetExitCode() != 0 {
		msg := strings.TrimSpace(resp.GetStderr())
		if msg == "" {
			msg = strings.TrimSpace(resp.GetStdout())
		}
		return guiSession{}, false, fmt.Errorf("query gui sessions: %s", msg)
	}
	return parseLinuxLoginctlSessions([]byte(resp.GetStdout()))
}

func probeMacOSGUISessionControl(client *ControlClient) (guiSession, bool, error) {
	resp, err := client.AgentExecTypedTimeout([]string{"sh", "-lc", macOSGUISessionScript()}, nil, "", 5*time.Second)
	if err != nil {
		return guiSession{}, false, fmt.Errorf("query gui session: %w", err)
	}
	if resp.GetExitCode() != 0 {
		return guiSession{}, false, nil
	}
	user := strings.TrimSpace(resp.GetStdout())
	if user == "" {
		return guiSession{}, false, nil
	}
	return guiSession{User: user, Seat: "console", Kind: "console"}, true, nil
}
