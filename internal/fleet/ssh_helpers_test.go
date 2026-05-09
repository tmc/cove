package fleet

import (
	"context"
	"path/filepath"
	"testing"
)

func TestRemoteControlSocketPath(t *testing.T) {
	tests := []struct {
		name   string
		remote Remote
		vm     string
		want   string
	}{
		{name: "explicit user", remote: Remote{User: "alice"}, vm: "dev", want: "/Users/alice/.vz/vms/dev/control.sock"},
		{name: "missing user falls back to placeholder", remote: Remote{}, vm: "dev", want: "/Users/$USER/.vz/vms/dev/control.sock"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RemoteControlSocketPath(tt.remote, tt.vm); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
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
