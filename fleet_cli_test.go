package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	fleetpkg "github.com/tmc/vz-macos/internal/fleet"
)

func TestFleetAddListRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fleet.json")
	if err := runFleetCommand([]string{"add", "demo", "me@localhost", "-vm", "ubuntu"}, path, &bytes.Buffer{}); err != nil {
		t.Fatalf("fleet add: %v", err)
	}
	var out bytes.Buffer
	if err := runFleetCommand([]string{"ls"}, path, &out); err != nil {
		t.Fatalf("fleet ls: %v", err)
	}
	got := out.String()
	for _, want := range []string{"demo", "me@localhost", "default_vm=ubuntu"} {
		if !strings.Contains(got, want) {
			t.Fatalf("fleet ls missing %q:\n%s", want, got)
		}
	}
}

func TestFleetRemove(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fleet.json")
	if err := runFleetCommand([]string{"add", "demo", "me@localhost"}, path, &bytes.Buffer{}); err != nil {
		t.Fatalf("fleet add: %v", err)
	}
	if err := runFleetCommand([]string{"rm", "demo"}, path, &bytes.Buffer{}); err != nil {
		t.Fatalf("fleet rm: %v", err)
	}
	var out bytes.Buffer
	if err := runFleetCommand([]string{"ls"}, path, &out); err != nil {
		t.Fatalf("fleet ls: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "no fleet remotes") {
		t.Fatalf("fleet ls after rm = %q", got)
	}
}

func TestFleetRemoteArgs(t *testing.T) {
	remote := fleetpkg.Remote{DefaultVM: "ubuntu"}
	for _, tc := range []struct {
		name string
		cmd  string
		args []string
		want []string
	}{
		{name: "ctl default vm", cmd: "ctl", args: []string{"gui", "status"}, want: []string{"ctl", "-vm", "ubuntu", "gui", "status"}},
		{name: "ctl explicit vm", cmd: "ctl", args: []string{"-vm", "other", "ping"}, want: []string{"ctl", "-vm", "other", "ping"}},
		{name: "vm list", cmd: "vm", args: []string{"list"}, want: []string{"vm", "list"}},
		{name: "top list", cmd: "list", want: []string{"list"}},
		{name: "image list", cmd: "image", args: []string{"list"}, want: []string{"image", "list"}},
		{name: "logs default vm", cmd: "logs", want: []string{"logs", "ubuntu"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := fleetRemoteArgs(tc.cmd, tc.args, remote)
			if err != nil {
				t.Fatalf("fleetRemoteArgs: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("fleetRemoteArgs = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestRunFleetRouteInvokesRunner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fleet.json")
	cfg := &fleetpkg.Config{}
	if err := cfg.Add("demo", fleetpkg.Remote{Host: "localhost", User: "me", DefaultVM: "ubuntu"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := fleetpkg.SavePath(path, cfg); err != nil {
		t.Fatalf("SavePath: %v", err)
	}
	runner := &fakeFleetRunner{}
	if err := runFleetRoute(context.Background(), "demo", "vm", []string{"list"}, path, runner, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("runFleetRoute: %v", err)
	}
	if runner.remote.Host != "localhost" || runner.remote.User != "me" {
		t.Fatalf("runner remote = %#v", runner.remote)
	}
	if want := []string{"vm", "list"}; !reflect.DeepEqual(runner.args, want) {
		t.Fatalf("runner args = %#v, want %#v", runner.args, want)
	}
}

func TestRunFleetRouteWithTrueSSH(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fleet.json")
	cfg := &fleetpkg.Config{}
	if err := cfg.Add("demo", fleetpkg.Remote{Host: "localhost", User: "me", DefaultVM: "ubuntu"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := fleetpkg.SavePath(path, cfg); err != nil {
		t.Fatalf("SavePath: %v", err)
	}
	truePath := "/bin/true"
	if _, err := os.Stat(truePath); err != nil {
		var lookErr error
		truePath, lookErr = exec.LookPath("true")
		if lookErr != nil {
			t.Skip("true command unavailable")
		}
	}
	t.Setenv("COVE_FLEET_SSH", truePath)
	if err := runFleetRoute(context.Background(), "demo", "vm", []string{"list"}, path, sshFleetRunner{}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("runFleetRoute with /bin/true ssh: %v", err)
	}
}

type fakeFleetRunner struct {
	remote fleetpkg.Remote
	args   []string
}

func (f *fakeFleetRunner) Run(ctx context.Context, remote fleetpkg.Remote, args []string, stdout, stderr io.Writer) error {
	f.remote = remote
	f.args = append([]string(nil), args...)
	return nil
}
