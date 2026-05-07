package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/tmc/vz-macos/internal/controlserver"
	agentpb "github.com/tmc/vz-macos/proto/agentpb"
)

type linuxLoginctlSession struct {
	ID    string `json:"session"`
	User  string `json:"user"`
	Name  string `json:"name"`
	Seat  string `json:"seat"`
	State string `json:"state"`
	Type  string `json:"type"`
}

type guiSessionExec interface {
	Exec(ctx context.Context, args []string, env map[string]string, workDir string) (*agentpb.ExecResponse, error)
}

func probeLinuxGUISession(ctx context.Context, exec guiSessionExec) (controlserver.GUISession, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	resp, err := exec.Exec(ctx, []string{"loginctl", "list-sessions", "--output", "json", "--no-pager"}, nil, "")
	if err != nil {
		return controlserver.GUISession{}, false, fmt.Errorf("query gui sessions: %w", err)
	}
	if resp.GetExitCode() != 0 {
		msg := strings.TrimSpace(string(resp.GetStderr()))
		if msg == "" {
			msg = strings.TrimSpace(string(resp.GetStdout()))
		}
		return controlserver.GUISession{}, false, fmt.Errorf("query gui sessions: %s", msg)
	}
	rows, err := parseLinuxLoginctlSessionRows(resp.GetStdout())
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
		show, err := exec.Exec(ctx, []string{"loginctl", "show-session", row.ID, "-p", "Name", "-p", "User", "-p", "Seat", "-p", "State", "-p", "Type", "--no-pager"}, nil, "")
		if err != nil || show.GetExitCode() != 0 {
			continue
		}
		if session, ok := parseLoginctlShowGUISession(row.ID, string(show.GetStdout())); ok {
			return session, true, nil
		}
	}
	return controlserver.GUISession{}, false, nil
}

func probeMacOSGUISession(ctx context.Context, exec guiSessionExec) (controlserver.GUISession, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	resp, err := exec.Exec(ctx, []string{"sh", "-lc", macOSGUISessionScript()}, nil, "")
	if err != nil {
		return controlserver.GUISession{}, false, fmt.Errorf("query gui session: %w", err)
	}
	if resp.GetExitCode() != 0 {
		return controlserver.GUISession{}, false, nil
	}
	user := strings.TrimSpace(string(resp.GetStdout()))
	if user == "" {
		return controlserver.GUISession{}, false, nil
	}
	return controlserver.GUISession{User: user, Seat: "console", Kind: "console"}, true, nil
}

func macOSGUISessionScript() string {
	return `user=$(stat -f %Su /dev/console) || exit 1
uid=$(stat -f %u /dev/console) || exit 1
if [ "$user" = root ] || [ "$uid" = 0 ]; then
	exit 2
fi
launchctl print "gui/$uid" >/dev/null || exit 3
printf '%s\n' "$user"`
}

func parseLinuxLoginctlSessions(data []byte) (controlserver.GUISession, bool, error) {
	rows, err := parseLinuxLoginctlSessionRows(data)
	if err != nil {
		return controlserver.GUISession{}, false, err
	}
	session, ok := activeGraphicalLoginctlSession(rows)
	return session, ok, nil
}

func parseLinuxLoginctlSessionRows(data []byte) ([]linuxLoginctlSession, error) {
	var rows []linuxLoginctlSession
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, fmt.Errorf("parse loginctl sessions: %w", err)
	}
	return rows, nil
}

func activeGraphicalLoginctlSession(rows []linuxLoginctlSession) (controlserver.GUISession, bool) {
	for _, row := range rows {
		kind := strings.ToLower(strings.TrimSpace(row.Type))
		if row.State != "active" || (kind != "wayland" && kind != "x11") {
			continue
		}
		user := strings.TrimSpace(row.User)
		if user == "" {
			user = strings.TrimSpace(row.Name)
		}
		if user == "" {
			continue
		}
		return controlserver.GUISession{
			ID:   row.ID,
			User: user,
			Seat: strings.TrimSpace(row.Seat),
			Kind: kind,
		}, true
	}
	return controlserver.GUISession{}, false
}

func parseLoginctlShowGUISession(id, out string) (controlserver.GUISession, bool) {
	props := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		key, val, ok := strings.Cut(line, "=")
		if ok {
			props[key] = strings.TrimSpace(val)
		}
	}
	kind := strings.ToLower(props["Type"])
	if props["State"] != "active" || (kind != "wayland" && kind != "x11") {
		return controlserver.GUISession{}, false
	}
	user := props["Name"]
	if user == "" {
		user = props["User"]
	}
	if user == "" {
		return controlserver.GUISession{}, false
	}
	return controlserver.GUISession{ID: id, User: user, Seat: props["Seat"], Kind: kind}, true
}
