package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/tmc/vz-macos/internal/controlserver"
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

	status := controlserver.AgentHealthState{DaemonStatus: "connected", UserStatus: "unknown"}
	if agentStatus, err := client.AgentExecTypedTimeout([]string{"true"}, nil, "", 5*time.Second); err != nil || agentStatus.GetExitCode() != 0 {
		status.DaemonStatus = "disconnected"
	}
	if status.DaemonStatus == "connected" {
		switch osName {
		case guestOSLinux:
			session, ok, err := probeLinuxGUISessionControl(client)
			if err != nil {
				return err
			}
			status.GUISession = session
			status.GUISessionActive = ok
		case guestOSDarwin:
			session, ok, err := probeMacOSGUISessionControl(client)
			if err != nil {
				return err
			}
			status.GUISession = session
			status.GUISessionActive = ok
		}
	}
	fmt.Println(controlserver.AgentHealthSummary(status))
	return nil
}

func probeLinuxGUISessionControl(client *ControlClient) (controlserver.GUISession, bool, error) {
	resp, err := client.AgentExecTypedTimeout([]string{"loginctl", "list-sessions", "--output", "json", "--no-pager"}, nil, "", 5*time.Second)
	if err != nil {
		return controlserver.GUISession{}, false, fmt.Errorf("query gui sessions: %w", err)
	}
	if resp.GetExitCode() != 0 {
		msg := strings.TrimSpace(resp.GetStderr())
		if msg == "" {
			msg = strings.TrimSpace(resp.GetStdout())
		}
		return controlserver.GUISession{}, false, fmt.Errorf("query gui sessions: %s", msg)
	}
	rows, err := parseLinuxLoginctlSessionRows([]byte(resp.GetStdout()))
	if err != nil {
		return controlserver.GUISession{}, false, err
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
	return controlserver.GUISession{}, false, nil
}

func probeMacOSGUISessionControl(client *ControlClient) (controlserver.GUISession, bool, error) {
	resp, err := client.AgentExecTypedTimeout([]string{"sh", "-lc", macOSGUISessionScript()}, nil, "", 5*time.Second)
	if err != nil {
		return controlserver.GUISession{}, false, fmt.Errorf("query gui session: %w", err)
	}
	if resp.GetExitCode() != 0 {
		return controlserver.GUISession{}, false, nil
	}
	user := strings.TrimSpace(resp.GetStdout())
	if user == "" {
		return controlserver.GUISession{}, false, nil
	}
	return controlserver.GUISession{User: user, Seat: "console", Kind: "console"}, true, nil
}
