package fleet

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoteControlSocketPath(t *testing.T) {
	tests := []struct {
		name   string
		remote Remote
		vm     string
		want   string
	}{
		{name: "explicit user still uses remote home", remote: Remote{User: "alice"}, vm: "dev", want: filepath.Join(".vz", "vms", "dev", "control.sock")},
		{name: "missing user uses remote home", remote: Remote{}, vm: "dev", want: filepath.Join(".vz", "vms", "dev", "control.sock")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RemoteControlSocketPath(tt.remote, tt.vm); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRemoteControlTokenPath(t *testing.T) {
	got := RemoteControlTokenPath(Remote{User: "alice"}, "dev")
	want := filepath.Join(".vz", "vms", "dev", "control.token")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestReadControlToken(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "args")
	ssh := filepath.Join(dir, "ssh")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + log + "\nprintf ' secret-token\\n'\n"
	if err := os.WriteFile(ssh, []byte(script), 0755); err != nil {
		t.Fatalf("write ssh stub: %v", err)
	}
	t.Setenv("COVE_FLEET_SSH", ssh)
	t.Setenv("COVE_FLEET_SSH_MULTIPLEX", "")
	token, err := ReadControlToken(context.Background(), Remote{User: "me", Host: "host"}, "dev")
	if err != nil {
		t.Fatalf("ReadControlToken: %v", err)
	}
	if token != "secret-token" {
		t.Fatalf("token = %q, want secret-token", token)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("read args log: %v", err)
	}
	want := strings.Join(append(wantSSHBaseArgs(), "me@host", "cat", filepath.Join(".vz", "vms", "dev", "control.token")), "\n")
	if strings.TrimSpace(string(data)) != want {
		t.Fatalf("ssh args = %q, want %q", strings.TrimSpace(string(data)), want)
	}
}

func TestSSHBaseArgs(t *testing.T) {
	t.Setenv("COVE_FLEET_SSH_MULTIPLEX", "")
	remote := Remote{SSHArgs: []string{"-o", "BatchMode=yes"}}
	got := SSHBaseArgs(remote)
	want := wantSSHBaseArgs("-o", "BatchMode=yes")
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("SSHBaseArgs = %#v, want %#v", got, want)
	}
}

func TestSSHBaseArgsMultiplexDisabled(t *testing.T) {
	t.Setenv("COVE_FLEET_SSH_MULTIPLEX", "0")
	got := SSHBaseArgs(Remote{SSHArgs: []string{"-p", "2222"}})
	want := []string{"-p", "2222"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("SSHBaseArgs = %#v, want %#v", got, want)
	}
}

func wantSSHBaseArgs(extra ...string) []string {
	args := []string{
		"-o", "ControlMaster=auto",
		"-o", "ControlPersist=60s",
		"-o", "ControlPath=" + filepath.Join(os.TempDir(), fmt.Sprintf("cove-fleet-%d-%%C", os.Getuid())),
	}
	args = append(args, extra...)
	return args
}

func TestRemoteTarget(t *testing.T) {
	tests := []struct {
		name   string
		remote Remote
		want   string
	}{
		{name: "user@host", remote: Remote{User: "tmc", Host: "mini.local"}, want: "tmc@mini.local"},
		{name: "host only", remote: Remote{Host: "mini.local"}, want: "mini.local"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := remoteTarget(tt.remote); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDefaultVM(t *testing.T) {
	tests := []struct {
		name   string
		remote Remote
		vm     string
		want   string
	}{
		{name: "explicit wins", remote: Remote{DefaultVM: "default"}, vm: "explicit", want: "explicit"},
		{name: "fallback to remote default", remote: Remote{DefaultVM: "default"}, vm: "", want: "default"},
		{name: "both empty", remote: Remote{}, vm: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := defaultVM(tt.remote, tt.vm); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSSHBinary(t *testing.T) {
	t.Run("env override", func(t *testing.T) {
		t.Setenv("COVE_FLEET_SSH", "/opt/custom/ssh")
		if got := sshBinary(); got != "/opt/custom/ssh" {
			t.Errorf("got %q, want %q", got, "/opt/custom/ssh")
		}
	})
	t.Run("default", func(t *testing.T) {
		t.Setenv("COVE_FLEET_SSH", "")
		if got := sshBinary(); got != "ssh" {
			t.Errorf("got %q, want %q", got, "ssh")
		}
	})
}

func TestWaitUnixSocketContextCancel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.sock")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	conn, err := waitUnixSocket(ctx, path)
	if err == nil {
		conn.Close()
		t.Fatal("want error from cancelled context")
	}
	if err != context.Canceled {
		t.Errorf("got %v, want context.Canceled", err)
	}
}
