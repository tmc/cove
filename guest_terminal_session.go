package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

type guestTerminalAgent interface {
	AgentExecTypedTimeout(args []string, env map[string]string, workDir string, timeout time.Duration) (*controlpb.AgentExecResponse, error)
}

type linuxGraphicalSession struct {
	ID      string
	User    string
	UID     int
	Seat    string
	Type    string
	Display string
	Env     []string
}

func findLinuxGraphicalSession(client guestTerminalAgent, wantUser string) (linuxGraphicalSession, error) {
	resp, err := client.AgentExecTypedTimeout([]string{"loginctl", "list-sessions", "--no-legend", "--no-pager"}, nil, "", 10*time.Second)
	if err != nil {
		return linuxGraphicalSession{}, fmt.Errorf("query graphical sessions: %w", err)
	}
	if resp.GetExitCode() != 0 {
		return linuxGraphicalSession{}, fmt.Errorf("query graphical sessions: %s", strings.TrimSpace(resp.GetStderr()))
	}
	ids := parseLoginctlSessionIDs(resp.GetStdout())
	for _, id := range ids {
		show, err := client.AgentExecTypedTimeout([]string{
			"loginctl", "show-session", id,
			"-p", "Name", "-p", "User", "-p", "Seat", "-p", "State", "-p", "Type", "-p", "Display",
			"--no-pager",
		}, nil, "", 10*time.Second)
		if err != nil || show.GetExitCode() != 0 {
			continue
		}
		session, ok := parseLoginctlShowSession(id, show.GetStdout())
		if !ok {
			continue
		}
		if wantUser != "" && session.User != wantUser {
			continue
		}
		return session, nil
	}
	if strings.TrimSpace(wantUser) != "" {
		return linuxGraphicalSession{}, fmt.Errorf("no active graphical session found for user %s on seat0", wantUser)
	}
	return linuxGraphicalSession{}, fmt.Errorf("no active graphical session found on seat0")
}

func parseLoginctlSessionIDs(out string) []string {
	var ids []string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 {
			ids = append(ids, fields[0])
		}
	}
	return ids
}

func parseLoginctlShowSession(id, out string) (linuxGraphicalSession, bool) {
	props := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		key, val, ok := strings.Cut(line, "=")
		if ok {
			props[key] = strings.TrimSpace(val)
		}
	}
	if props["State"] != "active" || props["Seat"] != "seat0" {
		return linuxGraphicalSession{}, false
	}
	sessionType := props["Type"]
	if sessionType != "wayland" && sessionType != "x11" {
		return linuxGraphicalSession{}, false
	}
	user := props["Name"]
	uid, err := strconv.Atoi(props["User"])
	if user == "" || err != nil || uid <= 0 {
		return linuxGraphicalSession{}, false
	}
	session := linuxGraphicalSession{
		ID:      id,
		User:    user,
		UID:     uid,
		Seat:    props["Seat"],
		Type:    sessionType,
		Display: props["Display"],
	}
	session.Env = linuxSessionEnv(session)
	return session, true
}

func linuxSessionEnv(session linuxGraphicalSession) []string {
	runtimeDir := fmt.Sprintf("/run/user/%d", session.UID)
	env := []string{
		"XDG_RUNTIME_DIR=" + runtimeDir,
		"DBUS_SESSION_BUS_ADDRESS=unix:path=" + runtimeDir + "/bus",
	}
	switch session.Type {
	case "wayland":
		display := session.Display
		if display == "" {
			display = "wayland-0"
		}
		env = append(env, "WAYLAND_DISPLAY="+display)
	case "x11":
		display := session.Display
		if display == "" {
			display = ":0"
		}
		env = append(env, "DISPLAY="+display)
	}
	return env
}
