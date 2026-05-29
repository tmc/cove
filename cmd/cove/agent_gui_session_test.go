package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/controlserver"
	agentpb "github.com/tmc/cove/proto/agentpb"
)

type fakeGUIExec struct {
	calls    int
	respFunc func(call int, args []string) (*agentpb.ExecResponse, error)
}

func (f *fakeGUIExec) Exec(ctx context.Context, args []string, env map[string]string, workDir string) (*agentpb.ExecResponse, error) {
	f.calls++
	return f.respFunc(f.calls, args)
}

func execOK(stdout string) *agentpb.ExecResponse {
	return &agentpb.ExecResponse{ExitCode: 0, Stdout: []byte(stdout)}
}

func TestProbeLinuxGUISessionFromList(t *testing.T) {
	exec := &fakeGUIExec{respFunc: func(call int, args []string) (*agentpb.ExecResponse, error) {
		return execOK(`[{"session":"7","uid":1000,"user":"u","seat":"seat0","state":"active","type":"wayland"}]`), nil
	}}
	sess, ok, err := probeLinuxGUISession(context.Background(), exec)
	if err != nil || !ok {
		t.Fatalf("err=%v ok=%v", err, ok)
	}
	want := controlserver.GUISession{ID: "7", User: "u", Seat: "seat0", Kind: "wayland"}
	if sess != want {
		t.Fatalf("sess=%#v want %#v", sess, want)
	}
}

func TestProbeLinuxGUISessionShowFallback(t *testing.T) {
	exec := &fakeGUIExec{respFunc: func(call int, args []string) (*agentpb.ExecResponse, error) {
		if call == 1 {
			return execOK(`[{"session":"9","uid":1000,"user":"","name":"","seat":"seat0","state":"active","type":"tty"}]`), nil
		}
		return execOK("Name=alice\nUser=1000\nSeat=seat0\nState=active\nType=x11\n"), nil
	}}
	sess, ok, err := probeLinuxGUISession(context.Background(), exec)
	if err != nil || !ok {
		t.Fatalf("err=%v ok=%v", err, ok)
	}
	if sess.User != "alice" || sess.Kind != "x11" || sess.ID != "9" {
		t.Fatalf("sess=%#v", sess)
	}
}

func TestProbeLinuxGUISessionExecError(t *testing.T) {
	exec := &fakeGUIExec{respFunc: func(call int, args []string) (*agentpb.ExecResponse, error) {
		return nil, errors.New("boom")
	}}
	if _, _, err := probeLinuxGUISession(context.Background(), exec); err == nil {
		t.Fatal("expected error")
	}
}

func TestProbeLinuxGUISessionNonZeroExit(t *testing.T) {
	exec := &fakeGUIExec{respFunc: func(call int, args []string) (*agentpb.ExecResponse, error) {
		return &agentpb.ExecResponse{ExitCode: 1, Stderr: []byte("nope")}, nil
	}}
	if _, _, err := probeLinuxGUISession(context.Background(), exec); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("err=%v", err)
	}
}

func TestProbeMacOSGUISessionOK(t *testing.T) {
	exec := &fakeGUIExec{respFunc: func(call int, args []string) (*agentpb.ExecResponse, error) {
		return execOK("alice\n"), nil
	}}
	sess, ok, err := probeMacOSGUISession(context.Background(), exec)
	if err != nil || !ok {
		t.Fatalf("err=%v ok=%v", err, ok)
	}
	want := controlserver.GUISession{User: "alice", Seat: "console", Kind: "console"}
	if sess != want {
		t.Fatalf("sess=%#v want %#v", sess, want)
	}
}

func TestProbeMacOSGUISessionNoUser(t *testing.T) {
	exec := &fakeGUIExec{respFunc: func(call int, args []string) (*agentpb.ExecResponse, error) {
		return execOK("   \n"), nil
	}}
	if _, ok, err := probeMacOSGUISession(context.Background(), exec); err != nil || ok {
		t.Fatalf("err=%v ok=%v", err, ok)
	}
}

func TestProbeMacOSGUISessionNonZeroExit(t *testing.T) {
	exec := &fakeGUIExec{respFunc: func(call int, args []string) (*agentpb.ExecResponse, error) {
		return &agentpb.ExecResponse{ExitCode: 2}, nil
	}}
	if _, ok, err := probeMacOSGUISession(context.Background(), exec); err != nil || ok {
		t.Fatalf("err=%v ok=%v", err, ok)
	}
}

func TestProbeMacOSGUISessionExecError(t *testing.T) {
	exec := &fakeGUIExec{respFunc: func(call int, args []string) (*agentpb.ExecResponse, error) {
		return nil, errors.New("boom")
	}}
	if _, _, err := probeMacOSGUISession(context.Background(), exec); err == nil {
		t.Fatal("expected error")
	}
}

func TestMacOSGUISessionScriptShape(t *testing.T) {
	s := macOSGUISessionScript()
	for _, sub := range []string{"/dev/console", "launchctl print", "exit 2", "exit 3"} {
		if !strings.Contains(s, sub) {
			t.Fatalf("missing %q in script", sub)
		}
	}
}

func TestParseLinuxLoginctlSessionsActiveWayland(t *testing.T) {
	in := []byte(`[
		{"session":"1","uid":1000,"user":"desk","seat":"seat0","state":"active","type":"wayland"},
		{"session":"2","uid":120,"user":"gdm","seat":"seat0","state":"online","type":"wayland"}
	]`)
	got, ok, err := parseLinuxLoginctlSessions(in)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("no session found")
	}
	want := controlserver.GUISession{ID: "1", User: "desk", Seat: "seat0", Kind: "wayland"}
	if got != want {
		t.Fatalf("session = %#v, want %#v", got, want)
	}
}

func TestParseLinuxLoginctlSessionsActiveX11NameFallback(t *testing.T) {
	in := []byte(`[
		{"session":"3","uid":1000,"name":"qa","seat":"seat0","state":"active","type":"x11"}
	]`)
	got, ok, err := parseLinuxLoginctlSessions(in)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("no session found")
	}
	want := controlserver.GUISession{ID: "3", User: "qa", Seat: "seat0", Kind: "x11"}
	if got != want {
		t.Fatalf("session = %#v, want %#v", got, want)
	}
}

func TestParseLinuxLoginctlSessionsSkipsNonGraphical(t *testing.T) {
	in := []byte(`[
		{"session":"1","uid":1000,"user":"desk","seat":"seat0","state":"online","type":"wayland"},
		{"session":"2","uid":1000,"user":"desk","seat":"seat0","state":"active","type":"tty"}
	]`)
	_, ok, err := parseLinuxLoginctlSessions(in)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("found non-graphical session")
	}
}

func TestParseLinuxLoginctlSessionsRejectsMalformedJSON(t *testing.T) {
	if _, _, err := parseLinuxLoginctlSessions([]byte(`not-json`)); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestParseLoginctlShowGUISession(t *testing.T) {
	got, ok := parseLoginctlShowGUISession("1", "Name=desk\nUser=1000\nSeat=seat0\nState=active\nType=wayland\n")
	if !ok {
		t.Fatal("no session found")
	}
	want := controlserver.GUISession{ID: "1", User: "desk", Seat: "seat0", Kind: "wayland"}
	if got != want {
		t.Fatalf("session = %#v, want %#v", got, want)
	}
}
