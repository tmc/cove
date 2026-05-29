package fleet

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func containsFlagValue(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

func TestMuxEnabled(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want bool
	}{
		{name: "default on", env: "", want: true},
		{name: "opt out with 1", env: "1", want: false},
		{name: "opt out with any value", env: "yes", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(muxDisableEnv, tt.env)
			if got := MuxEnabled(); got != tt.want {
				t.Fatalf("MuxEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSSHMuxOptionsContainsMuxFlags(t *testing.T) {
	t.Setenv(muxDisableEnv, "")
	t.Setenv("HOME", t.TempDir())
	remote := Remote{User: "tmc", Host: "mini.local"}
	opts := SSHMuxOptions(remote)
	if !containsFlagValue(opts, "-o", "ControlMaster=auto") {
		t.Errorf("missing ControlMaster=auto in %#v", opts)
	}
	if !containsFlagValue(opts, "-o", "ControlPersist="+MuxControlPersist) {
		t.Errorf("missing ControlPersist in %#v", opts)
	}
	if !containsFlagValue(opts, "-o", "ControlPath="+MuxControlPath(remote)) {
		t.Errorf("missing ControlPath in %#v", opts)
	}
}

func TestSSHMuxOptionsDisabled(t *testing.T) {
	t.Setenv(muxDisableEnv, "1")
	if opts := SSHMuxOptions(Remote{Host: "h"}); opts != nil {
		t.Fatalf("SSHMuxOptions() = %#v, want nil when disabled", opts)
	}
}

func TestMuxControlPathStablePerHost(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	remote := Remote{User: "tmc", Host: "mini.local"}
	first := MuxControlPath(remote)
	second := MuxControlPath(remote)
	if first != second {
		t.Fatalf("control path not stable: %q vs %q", first, second)
	}
}

func TestMuxControlPathDistinctAcrossHosts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tests := []struct {
		name string
		a, b Remote
	}{
		{name: "different host", a: Remote{User: "tmc", Host: "a.local"}, b: Remote{User: "tmc", Host: "b.local"}},
		{name: "different user same host", a: Remote{User: "alice", Host: "h"}, b: Remote{User: "bob", Host: "h"}},
		{name: "user vs no user", a: Remote{Host: "h"}, b: Remote{User: "tmc", Host: "h"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if MuxControlPath(tt.a) == MuxControlPath(tt.b) {
				t.Fatalf("control paths collide for %v and %v", tt.a, tt.b)
			}
		})
	}
}

func TestMuxControlPathHonorsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := MuxControlPath(Remote{Host: "h"})
	wantPrefix := filepath.Join(home, ".vz", "fleet-ssh")
	if !strings.HasPrefix(path, wantPrefix) {
		t.Fatalf("control path %q, want prefix %q", path, wantPrefix)
	}
}

func TestSSHForwardArgsInjectsMux(t *testing.T) {
	t.Setenv(muxDisableEnv, "")
	t.Setenv("HOME", t.TempDir())
	remote := Remote{User: "tmc", Host: "mini.local", SSHArgs: []string{"-p", "2222"}}
	args := SSHForwardArgs(remote, "/tmp/local.sock", "remote.sock")
	if !containsFlagValue(args, "-o", "ControlMaster=auto") {
		t.Errorf("SSHForwardArgs missing mux flags: %#v", args)
	}
	// User-provided SSHArgs and the target must still be present.
	if !containsFlagValue(args, "-p", "2222") {
		t.Errorf("SSHForwardArgs dropped user ssh args: %#v", args)
	}
	if args[len(args)-1] != "tmc@mini.local" {
		t.Errorf("SSHForwardArgs target = %q, want tmc@mini.local", args[len(args)-1])
	}
}

func TestSSHForwardArgsNoMuxWhenDisabled(t *testing.T) {
	t.Setenv(muxDisableEnv, "1")
	args := SSHForwardArgs(Remote{Host: "h"}, "/tmp/l.sock", "r.sock")
	for _, a := range args {
		if strings.HasPrefix(a, "ControlMaster") || strings.HasPrefix(a, "ControlPath") {
			t.Fatalf("SSHForwardArgs injected mux flags while disabled: %#v", args)
		}
	}
}

func TestReadControlTokenInjectsMux(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(muxDisableEnv, "")
	t.Setenv("HOME", dir)
	log := filepath.Join(dir, "args")
	ssh := filepath.Join(dir, "ssh")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + log + "\nprintf 'tok\\n'\n"
	if err := os.WriteFile(ssh, []byte(script), 0755); err != nil {
		t.Fatalf("write ssh stub: %v", err)
	}
	t.Setenv("COVE_FLEET_SSH", ssh)
	if _, err := ReadControlToken(context.Background(), Remote{User: "me", Host: "host"}, "dev"); err != nil {
		t.Fatalf("ReadControlToken: %v", err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("read args log: %v", err)
	}
	if !strings.Contains(string(data), "ControlMaster=auto") {
		t.Fatalf("ReadControlToken argv missing mux flags: %q", string(data))
	}
}

func TestEnsureMuxDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := EnsureMuxDir(); err != nil {
		t.Fatalf("EnsureMuxDir: %v", err)
	}
	if _, err := filepath.Glob(filepath.Join(home, ".vz", "fleet-ssh")); err != nil {
		t.Fatalf("glob: %v", err)
	}
}
