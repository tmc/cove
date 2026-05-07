package main

import (
	"fmt"
	"strings"
	"time"
)

func statusCommand() error {
	if !isVMRunningAt(vmDir) {
		state := detectVMState(vmDir)
		if state == "starting" {
			_, note := runtimeListFields(vmDir, state)
			fmt.Printf("starting: %s\n", note)
			return nil
		}
	}
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
	rows, err := parseLinuxLoginctlSessionRows([]byte(resp.GetStdout()))
	if err != nil {
		return guiSession{}, false, err
	}
	if session, ok := activeGraphicalLoginctlSession(rows); ok {
		return session, true, nil
	}
	for _, row := range rows {
		if row.State != "active" || row.ID == "" {
			continue
		}
		show, err := client.AgentExecTypedTimeout([]string{"loginctl", "show-session", row.ID, "-p", "Name", "-p", "User", "-p", "Seat", "-p", "State", "-p", "Type", "--no-pager"}, nil, "", 5*time.Second)
		if err != nil || show.GetExitCode() != 0 {
			continue
		}
		if session, ok := parseLoginctlShowGUISession(row.ID, show.GetStdout()); ok {
			return session, true, nil
		}
	}
	return guiSession{}, false, nil
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
