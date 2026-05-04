package main

import (
	"reflect"
	"strings"
	"testing"
	"time"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

type fakeGuestTerminalAgent struct {
	responses []*controlpb.AgentExecResponse
	args      [][]string
}

func (f *fakeGuestTerminalAgent) AgentExecTypedTimeout(args []string, env map[string]string, workDir string, timeout time.Duration) (*controlpb.AgentExecResponse, error) {
	f.args = append(f.args, append([]string(nil), args...))
	if len(f.responses) == 0 {
		return &controlpb.AgentExecResponse{ExitCode: 1, Stderr: "unexpected call"}, nil
	}
	resp := f.responses[0]
	f.responses = f.responses[1:]
	return resp, nil
}

func TestFindLinuxGraphicalSessionWayland(t *testing.T) {
	client := &fakeGuestTerminalAgent{responses: []*controlpb.AgentExecResponse{
		{ExitCode: 0, Stdout: "1 1000 desk seat0 tty2\n"},
		{ExitCode: 0, Stdout: "Name=desk\nUser=1000\nSeat=seat0\nState=active\nType=wayland\nDisplay=wayland-1\n"},
	}}
	got, err := findLinuxGraphicalSession(client, "desk")
	if err != nil {
		t.Fatalf("findLinuxGraphicalSession: %v", err)
	}
	if got.User != "desk" || got.UID != 1000 || got.Type != "wayland" {
		t.Fatalf("session = %+v, want desk uid 1000 wayland", got)
	}
	wantEnv := []string{
		"XDG_RUNTIME_DIR=/run/user/1000",
		"DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus",
		"WAYLAND_DISPLAY=wayland-1",
	}
	if !reflect.DeepEqual(got.Env, wantEnv) {
		t.Fatalf("env = %#v, want %#v", got.Env, wantEnv)
	}
}

func TestFindLinuxGraphicalSessionSkipsInactiveAndFindsX11(t *testing.T) {
	client := &fakeGuestTerminalAgent{responses: []*controlpb.AgentExecResponse{
		{ExitCode: 0, Stdout: "1 1000 desk seat0 tty2\n2 1001 qa seat0 tty3\n"},
		{ExitCode: 0, Stdout: "Name=desk\nUser=1000\nSeat=seat0\nState=closing\nType=wayland\n"},
		{ExitCode: 0, Stdout: "Name=qa\nUser=1001\nSeat=seat0\nState=active\nType=x11\n"},
	}}
	got, err := findLinuxGraphicalSession(client, "")
	if err != nil {
		t.Fatalf("findLinuxGraphicalSession: %v", err)
	}
	if got.User != "qa" || got.Type != "x11" {
		t.Fatalf("session = %+v, want qa x11", got)
	}
	if got.Env[2] != "DISPLAY=:0" {
		t.Fatalf("display env = %q, want DISPLAY=:0", got.Env[2])
	}
}

func TestFindLinuxGraphicalSessionMissingUser(t *testing.T) {
	client := &fakeGuestTerminalAgent{responses: []*controlpb.AgentExecResponse{
		{ExitCode: 0, Stdout: "1 1000 desk seat0 tty2\n"},
		{ExitCode: 0, Stdout: "Name=desk\nUser=1000\nSeat=seat0\nState=active\nType=wayland\n"},
	}}
	_, err := findLinuxGraphicalSession(client, "other")
	if err == nil {
		t.Fatal("findLinuxGraphicalSession succeeded; want error")
	}
	if !strings.Contains(err.Error(), "no active graphical session found for user other") {
		t.Fatalf("error = %q", err)
	}
}
