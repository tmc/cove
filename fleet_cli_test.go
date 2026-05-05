package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
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

func TestFleetVMLsAggregatesHosts(t *testing.T) {
	path := writeFleetTestConfig(t)
	runner := &fakeFleetRunner{outputs: map[string]string{
		"a.local": "a-vm running\n",
		"b.local": "b-vm stopped\n",
	}}
	var out bytes.Buffer
	if err := runFleetCommandWithRunner(context.Background(), []string{"vm", "ls"}, path, runner, &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("fleet vm ls: %v", err)
	}
	got := out.String()
	for _, want := range []string{"a\ta-vm running", "b\tb-vm stopped"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
	runner.assertCalls(t, []string{"vm", "list"}, 2)
}

func TestFleetImageLsJSONIncludesFailures(t *testing.T) {
	path := writeFleetTestConfig(t)
	runner := &fakeFleetRunner{
		outputs: map[string]string{"a.local": "image-a latest\n"},
		errs:    map[string]error{"b.local": errors.New("unreachable")},
	}
	var out bytes.Buffer
	if err := runFleetCommandWithRunner(context.Background(), []string{"image", "ls", "--json"}, path, runner, &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("fleet image ls --json: %v", err)
	}
	got := out.String()
	for _, want := range []string{`"host": "a"`, `"kind": "image"`, `"output": "image-a latest"`, `"host": "b"`, `"error": "unreachable"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("json missing %q:\n%s", want, got)
		}
	}
	runner.assertCalls(t, []string{"image", "list"}, 2)
}

func TestFleetPSFiltersRunningVMs(t *testing.T) {
	path := writeFleetTestConfig(t)
	runner := &fakeFleetRunner{outputs: map[string]string{
		"a.local": "a-vm running\nb-vm stopped\n",
		"b.local": "c-vm Running\n",
	}}
	var out bytes.Buffer
	if err := runFleetCommandWithRunner(context.Background(), []string{"ps"}, path, runner, &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("fleet ps: %v", err)
	}
	got := out.String()
	for _, want := range []string{"a\ta-vm running", "b\tc-vm Running"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "stopped") {
		t.Fatalf("output includes stopped VM:\n%s", got)
	}
	runner.assertCalls(t, []string{"vm", "list"}, 2)
}

func TestFleetPSJSONIncludesFailures(t *testing.T) {
	path := writeFleetTestConfig(t)
	runner := &fakeFleetRunner{
		outputs: map[string]string{"a.local": "a-vm running\nb-vm stopped\n"},
		errs:    map[string]error{"b.local": errors.New("unreachable")},
	}
	var out bytes.Buffer
	if err := runFleetCommandWithRunner(context.Background(), []string{"ps", "--json"}, path, runner, &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("fleet ps --json: %v", err)
	}
	got := out.String()
	for _, want := range []string{`"host": "a"`, `"kind": "ps"`, `"output": "a-vm running"`, `"host": "b"`, `"error": "unreachable"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("json missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "stopped") {
		t.Fatalf("json includes stopped VM:\n%s", got)
	}
	runner.assertCalls(t, []string{"vm", "list"}, 2)
}

func writeFleetTestConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fleet.json")
	cfg := &fleetpkg.Config{}
	if err := cfg.Add("a", fleetpkg.Remote{Host: "a.local"}); err != nil {
		t.Fatalf("Add a: %v", err)
	}
	if err := cfg.Add("b", fleetpkg.Remote{Host: "b.local"}); err != nil {
		t.Fatalf("Add b: %v", err)
	}
	if err := fleetpkg.SavePath(path, cfg); err != nil {
		t.Fatalf("SavePath: %v", err)
	}
	return path
}

type fakeFleetCall struct {
	remote fleetpkg.Remote
	args   []string
}

type fakeFleetRunner struct {
	mu      sync.Mutex
	remote  fleetpkg.Remote
	args    []string
	outputs map[string]string
	errs    map[string]error
	calls   []fakeFleetCall
}

func (f *fakeFleetRunner) Run(ctx context.Context, remote fleetpkg.Remote, args []string, stdout, stderr io.Writer) error {
	f.mu.Lock()
	f.remote = remote
	f.args = append([]string(nil), args...)
	f.calls = append(f.calls, fakeFleetCall{remote: remote, args: append([]string(nil), args...)})
	out := ""
	if f.outputs != nil {
		out = f.outputs[remote.Host]
	}
	err := error(nil)
	if f.errs != nil {
		err = f.errs[remote.Host]
	}
	f.mu.Unlock()
	if err != nil {
		return err
	}
	_, _ = io.WriteString(stdout, out)
	return nil
}

func (f *fakeFleetRunner) assertCalls(t *testing.T, wantArgs []string, wantCount int) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) != wantCount {
		t.Fatalf("call count = %d, want %d", len(f.calls), wantCount)
	}
	for _, call := range f.calls {
		if !reflect.DeepEqual(call.args, wantArgs) {
			t.Fatalf("call args = %#v, want %#v", call.args, wantArgs)
		}
	}
}
