package main

import (
	"reflect"
	"strings"
	"testing"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

func TestDetectGuestOS(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want string
	}{
		{"linux", "Linux\n", guestOSLinux},
		{"darwin", "Darwin\n", guestOSDarwin},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := &fakeGuestTerminalAgent{responses: []*controlpb.AgentExecResponse{{Stdout: tt.out}}}
			got, err := detectGuestOS(agent)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("detectGuestOS() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFindLinuxTerminalProgramPreference(t *testing.T) {
	agent := &fakeGuestTerminalAgent{responses: []*controlpb.AgentExecResponse{
		{ExitCode: 1},
		{Stdout: "/usr/bin/kgx\n"},
	}}
	got, err := findLinuxTerminalProgram(agent)
	if err != nil {
		t.Fatal(err)
	}
	if got != "kgx" {
		t.Fatalf("findLinuxTerminalProgram() = %q, want kgx", got)
	}
	if !reflect.DeepEqual(agent.args[0], []string{"/bin/sh", "-c", "command -v 'gnome-terminal'"}) {
		t.Fatalf("first lookup = %#v", agent.args[0])
	}
	if !reflect.DeepEqual(agent.args[1], []string{"/bin/sh", "-c", "command -v 'kgx'"}) {
		t.Fatalf("second lookup = %#v", agent.args[1])
	}
}

func TestFindLinuxTerminalProgramMissing(t *testing.T) {
	agent := &fakeGuestTerminalAgent{responses: []*controlpb.AgentExecResponse{
		{ExitCode: 1}, {ExitCode: 1}, {ExitCode: 1}, {ExitCode: 1},
	}}
	_, err := findLinuxTerminalProgram(agent)
	if err == nil || !strings.Contains(err.Error(), "no supported terminal installed") {
		t.Fatalf("findLinuxTerminalProgram() error = %v", err)
	}
}

func TestLinuxTerminalLaunchArgsWayland(t *testing.T) {
	session := linuxGraphicalSession{
		User: "desk",
		UID:  1000,
		Type: "wayland",
		Env: []string{
			"XDG_RUNTIME_DIR=/run/user/1000",
			"DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus",
			"WAYLAND_DISPLAY=wayland-0",
		},
	}
	got := linuxTerminalLaunchArgs(session, "gnome-terminal", []string{"bash", "-c", "echo COVE_T1_LIVE_OK; sleep 30"})
	want := []string{
		"runuser", "-u", "desk", "--", "env",
		"XDG_RUNTIME_DIR=/run/user/1000",
		"DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus",
		"WAYLAND_DISPLAY=wayland-0",
		"gnome-terminal", "--window", "--", "bash", "-lc", "'bash' '-c' 'echo COVE_T1_LIVE_OK; sleep 30'",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("linuxTerminalLaunchArgs() = %#v, want %#v", got, want)
	}
}

func TestLinuxTerminalLaunchArgsXTerm(t *testing.T) {
	session := linuxGraphicalSession{
		User: "desk",
		UID:  1000,
		Type: "x11",
		Env:  []string{"DISPLAY=:0"},
	}
	got := linuxTerminalLaunchArgs(session, "xterm", []string{"echo", "ok"})
	want := []string{"runuser", "-u", "desk", "--", "env", "DISPLAY=:0", "xterm", "-e", "bash", "-lc", "'echo' 'ok'"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("linuxTerminalLaunchArgs() = %#v, want %#v", got, want)
	}
}
