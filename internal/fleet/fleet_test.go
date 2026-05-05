package fleet

import (
	"context"
	"net"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fleet.json")
	cfg := &Config{}
	if err := cfg.Add("studio", Remote{
		Host:      "mac-studio.local",
		User:      "tmc",
		SSHArgs:   []string{"-i", "~/.ssh/cove"},
		DefaultVM: "ubuntu",
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := SavePath(path, cfg); err != nil {
		t.Fatalf("SavePath: %v", err)
	}
	got, err := LoadPath(path)
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	remote, ok := got.Get("studio")
	if !ok {
		t.Fatal("studio missing after load")
	}
	if remote.Host != "mac-studio.local" || remote.User != "tmc" || remote.DefaultVM != "ubuntu" {
		t.Fatalf("remote = %#v", remote)
	}
	if !reflect.DeepEqual(remote.SSHArgs, []string{"-i", "~/.ssh/cove"}) {
		t.Fatalf("ssh args = %#v", remote.SSHArgs)
	}
}

func TestConfigAddRemoveList(t *testing.T) {
	cfg := &Config{}
	if err := cfg.Add("b", Remote{Host: "b.local"}); err != nil {
		t.Fatalf("Add b: %v", err)
	}
	if err := cfg.Add("a", Remote{Host: "a.local"}); err != nil {
		t.Fatalf("Add a: %v", err)
	}
	if err := cfg.Add("a", Remote{Host: "other.local"}); err == nil {
		t.Fatal("duplicate Add succeeded")
	}
	list := cfg.List()
	if len(list) != 2 || list[0].Name != "a" || list[1].Name != "b" {
		t.Fatalf("List = %#v", list)
	}
	if err := cfg.Remove("a"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, ok := cfg.Get("a"); ok {
		t.Fatal("removed remote still present")
	}
}

func TestParseTarget(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Remote
	}{
		{in: "tmc@mini.local", want: Remote{User: "tmc", Host: "mini.local"}},
		{in: "mini.local", want: Remote{Host: "mini.local"}},
	} {
		got, err := ParseTarget(tc.in)
		if err != nil {
			t.Fatalf("ParseTarget(%q): %v", tc.in, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("ParseTarget(%q) = %#v, want %#v", tc.in, got, tc.want)
		}
	}
}

func TestSSHForwardArgs(t *testing.T) {
	remote := Remote{Host: "mini.local", User: "tmc", SSHArgs: []string{"-o", "BatchMode=yes"}}
	got := SSHForwardArgs(remote, "/tmp/local.sock", "/Users/tmc/.vz/vms/vm/control.sock")
	want := []string{"-N", "-L", "/tmp/local.sock:/Users/tmc/.vz/vms/vm/control.sock", "-o", "BatchMode=yes", "tmc@mini.local"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SSHForwardArgs = %#v, want %#v", got, want)
	}
}

func TestWaitUnixSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	accepted := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			conn.Close()
		}
		close(accepted)
	}()
	conn, err := waitUnixSocket(context.Background(), path)
	if err != nil {
		t.Fatalf("waitUnixSocket: %v", err)
	}
	conn.Close()
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("server did not accept connection")
	}
}
