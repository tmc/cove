package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	agentpb "github.com/tmc/vz-macos/proto/agentpb"
)

type guiSession struct {
	User string
	Seat string
	Kind string
}

type guiSessionExec interface {
	Exec(ctx context.Context, args []string, env map[string]string, workDir string) (*agentpb.ExecResponse, error)
}

func probeLinuxGUISession(ctx context.Context, exec guiSessionExec) (guiSession, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	resp, err := exec.Exec(ctx, []string{"loginctl", "list-sessions", "--output", "json", "--no-pager"}, nil, "")
	if err != nil {
		return guiSession{}, false, fmt.Errorf("query gui sessions: %w", err)
	}
	if resp.GetExitCode() != 0 {
		msg := strings.TrimSpace(string(resp.GetStderr()))
		if msg == "" {
			msg = strings.TrimSpace(string(resp.GetStdout()))
		}
		return guiSession{}, false, fmt.Errorf("query gui sessions: %s", msg)
	}
	return parseLinuxLoginctlSessions(resp.GetStdout())
}

func probeMacOSGUISession(ctx context.Context, exec guiSessionExec) (guiSession, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	resp, err := exec.Exec(ctx, []string{"sh", "-lc", macOSGUISessionScript()}, nil, "")
	if err != nil {
		return guiSession{}, false, fmt.Errorf("query gui session: %w", err)
	}
	if resp.GetExitCode() != 0 {
		return guiSession{}, false, nil
	}
	user := strings.TrimSpace(string(resp.GetStdout()))
	if user == "" {
		return guiSession{}, false, nil
	}
	return guiSession{User: user, Seat: "console", Kind: "console"}, true, nil
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

func parseLinuxLoginctlSessions(data []byte) (guiSession, bool, error) {
	var rows []struct {
		User  string `json:"user"`
		Name  string `json:"name"`
		Seat  string `json:"seat"`
		State string `json:"state"`
		Type  string `json:"type"`
	}
	if err := json.Unmarshal(data, &rows); err != nil {
		return guiSession{}, false, fmt.Errorf("parse loginctl sessions: %w", err)
	}
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
		return guiSession{
			User: user,
			Seat: strings.TrimSpace(row.Seat),
			Kind: kind,
		}, true, nil
	}
	return guiSession{}, false, nil
}
