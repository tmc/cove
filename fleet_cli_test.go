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

	fleetpkg "github.com/tmc/cove/internal/fleet"
)

type fakeReadWriteCloser struct {
	close func() error
}

func (f fakeReadWriteCloser) Read([]byte) (int, error)    { return 0, io.EOF }
func (f fakeReadWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (f fakeReadWriteCloser) Close() error {
	if f.close != nil {
		return f.close()
	}
	return nil
}

func TestFleetAddListRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fleet.json")
	if err := runFleetCommand([]string{"add", "demo", "me@localhost", "-vm", "ubuntu"}, path, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("fleet add: %v", err)
	}
	var out bytes.Buffer
	if err := runFleetCommand([]string{"ls"}, path, &out, &bytes.Buffer{}); err != nil {
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
	if err := runFleetCommand([]string{"add", "demo", "me@localhost"}, path, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("fleet add: %v", err)
	}
	if err := runFleetCommand([]string{"rm", "demo"}, path, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("fleet rm: %v", err)
	}
	var out bytes.Buffer
	if err := runFleetCommand([]string{"ls"}, path, &out, &bytes.Buffer{}); err != nil {
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
		{name: "vm list", cmd: "vm", args: []string{"list"}, want: []string{"vm", "list"}},
		{name: "top list", cmd: "list", want: []string{"list"}},
		{name: "image list", cmd: "image", args: []string{"list"}, want: []string{"image", "list"}},
		{name: "run", cmd: "run", args: []string{"-linux", "-headless"}, want: []string{"run", "-linux", "-headless"}},
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

func TestFleetRunLeastLoadedSelectsHost(t *testing.T) {
	path := writeFleetTestConfig(t)
	runner := &fakeFleetRunner{outputs: map[string]string{
		"a.local": "a1 running\na2 running\n",
		"b.local": "b1 running\n",
	}}
	var out bytes.Buffer
	if err := runFleetCommandWithRunner(context.Background(), []string{"run", "--policy=least-loaded", "-linux", "-headless"}, path, runner, &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("fleet run: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "selected b") {
		t.Fatalf("output = %q, want selected b", got)
	}
	runner.assertCallsWithArgs(t, []string{"vm", "list"}, 2)
	runner.assertSawCall(t, "b.local", []string{"run", "-linux", "-headless"})
}

func TestFleetRunLeastLoadedIgnoresUnreachable(t *testing.T) {
	path := writeFleetTestConfig(t)
	runner := &fakeFleetRunner{
		outputs: map[string]string{"b.local": "b1 running\n"},
		errs:    map[string]error{"a.local": errors.New("offline")},
	}
	var out bytes.Buffer
	if err := runFleetCommandWithRunner(context.Background(), []string{"run", "--policy=least-loaded"}, path, runner, &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("fleet run: %v", err)
	}
	if !strings.Contains(out.String(), "selected b") {
		t.Fatalf("output = %q, want selected b", out.String())
	}
	runner.assertSawCall(t, "b.local", []string{"run"})
}

func TestFleetRunRequiresOptInPolicy(t *testing.T) {
	err := runFleetCommandWithRunner(context.Background(), []string{"run"}, writeFleetTestConfig(t), &fakeFleetRunner{}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "least-loaded") {
		t.Fatalf("fleet run error = %v, want least-loaded usage", err)
	}
}

func TestFleetMetricsAggregatesHosts(t *testing.T) {
	path := writeFleetTestConfig(t)
	runner := &fakeFleetRunner{outputs: map[string]string{
		"a.local": "coved_vms_managed 2\ncoved_image_gc_runs_total 1\n",
		"b.local": "coved_vms_managed 3\ncoved_image_gc_runs_total 4\n",
	}}
	var out bytes.Buffer
	if err := runFleetCommandWithRunner(context.Background(), []string{"metrics"}, path, runner, &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("fleet metrics: %v", err)
	}
	got := out.String()
	for _, want := range []string{"a\tvms=2", "b\tvms=3", "total\tvms=5"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
	runner.assertCallsWithArgs(t, []string{"daemon", "metrics", "--json"}, 2)
}

func TestFleetMetricsJSONIncludesErrors(t *testing.T) {
	path := writeFleetTestConfig(t)
	runner := &fakeFleetRunner{
		outputs: map[string]string{"a.local": "coved_vms_managed 2\n"},
		errs:    map[string]error{"b.local": errors.New("offline")},
	}
	var out bytes.Buffer
	if err := runFleetCommandWithRunner(context.Background(), []string{"metrics", "--json"}, path, runner, &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("fleet metrics --json: %v", err)
	}
	got := out.String()
	for _, want := range []string{`"host": "a"`, `"coved_vms_managed": 2`, `"host": "b"`, `"error": "offline"`, `"totals"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("json missing %q:\n%s", want, got)
		}
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

func TestRunFleetRouteCtlUsesControlTunnel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fleet.json")
	cfg := &fleetpkg.Config{}
	if err := cfg.Add("demo", fleetpkg.Remote{Host: "localhost", User: "me", DefaultVM: "ubuntu"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := fleetpkg.SavePath(path, cfg); err != nil {
		t.Fatalf("SavePath: %v", err)
	}
	oldDial := fleetDialControlSocket
	oldToken := fleetReadControlToken
	oldCtl := fleetCtlCommand
	defer func() {
		fleetDialControlSocket = oldDial
		fleetReadControlToken = oldToken
		fleetCtlCommand = oldCtl
	}()
	var dialVM string
	closed := false
	fleetDialControlSocket = func(ctx context.Context, remote fleetpkg.Remote, vm string) (*fleetpkg.Tunnel, error) {
		dialVM = vm
		return &fleetpkg.Tunnel{
			ReadWriteCloser: fakeReadWriteCloser{close: func() error { closed = true; return nil }},
			LocalSocketPath: "/tmp/fleet-control.sock",
		}, nil
	}
	fleetReadControlToken = func(ctx context.Context, remote fleetpkg.Remote, vm string) (string, error) {
		if vm != "ubuntu" {
			t.Fatalf("token vm = %q, want ubuntu", vm)
		}
		return "secret", nil
	}
	var ctlArgs []string
	fleetCtlCommand = func(args []string) error {
		ctlArgs = append([]string(nil), args...)
		return nil
	}
	if err := runFleetRoute(context.Background(), "demo", "ctl", []string{"ping"}, path, &fakeFleetRunner{}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("runFleetRoute ctl: %v", err)
	}
	if dialVM != "ubuntu" {
		t.Fatalf("dial vm = %q, want ubuntu", dialVM)
	}
	if want := []string{"-socket", "/tmp/fleet-control.sock", "-token", "secret", "ping"}; !reflect.DeepEqual(ctlArgs, want) {
		t.Fatalf("ctl args = %#v, want %#v", ctlArgs, want)
	}
	if !closed {
		t.Fatal("tunnel was not closed")
	}
}

func TestFleetRemoteSubcommandRoutesCtl(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fleet.json")
	cfg := &fleetpkg.Config{}
	if err := cfg.Add("demo", fleetpkg.Remote{Host: "localhost", DefaultVM: "ubuntu"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := fleetpkg.SavePath(path, cfg); err != nil {
		t.Fatalf("SavePath: %v", err)
	}
	oldDial := fleetDialControlSocket
	oldToken := fleetReadControlToken
	oldCtl := fleetCtlCommand
	defer func() {
		fleetDialControlSocket = oldDial
		fleetReadControlToken = oldToken
		fleetCtlCommand = oldCtl
	}()
	fleetDialControlSocket = func(ctx context.Context, remote fleetpkg.Remote, vm string) (*fleetpkg.Tunnel, error) {
		if vm != "ubuntu" {
			t.Fatalf("dial vm = %q, want ubuntu", vm)
		}
		return &fleetpkg.Tunnel{
			ReadWriteCloser: fakeReadWriteCloser{},
			LocalSocketPath: "/tmp/fleet-control.sock",
		}, nil
	}
	fleetReadControlToken = func(ctx context.Context, remote fleetpkg.Remote, vm string) (string, error) {
		if vm != "ubuntu" {
			t.Fatalf("token vm = %q, want ubuntu", vm)
		}
		return "secret", nil
	}
	var ctlArgs []string
	fleetCtlCommand = func(args []string) error {
		ctlArgs = append([]string(nil), args...)
		return nil
	}
	if err := runFleetCommandWithRunner(context.Background(), []string{"demo", "ctl", "ping"}, path, &fakeFleetRunner{}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("fleet demo ctl ping: %v", err)
	}
	if want := []string{"-socket", "/tmp/fleet-control.sock", "-token", "secret", "ping"}; !reflect.DeepEqual(ctlArgs, want) {
		t.Fatalf("ctl args = %#v, want %#v", ctlArgs, want)
	}
}

func TestFleetControlVM(t *testing.T) {
	remote := fleetpkg.Remote{DefaultVM: "default"}
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "default", args: []string{"ping"}, want: "default"},
		{name: "short flag", args: []string{"-vm", "other", "ping"}, want: "other"},
		{name: "long flag", args: []string{"--vm", "other", "ping"}, want: "other"},
		{name: "short equals", args: []string{"-vm=other", "ping"}, want: "other"},
		{name: "long equals", args: []string{"--vm=other", "ping"}, want: "other"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fleetControlVM(tt.args, remote); got != tt.want {
				t.Fatalf("fleetControlVM(%v) = %q, want %q", tt.args, got, tt.want)
			}
		})
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

func TestFleetMultiHostCoordination(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		outputs   map[string]string
		errs      map[string]error
		wantOut   []string
		wantErr   string
		wantCalls int
	}{
		{
			name: "vm list aggregates three hosts",
			args: []string{"vm", "ls"},
			outputs: map[string]string{
				"a.local": "a-vm running\n",
				"b.local": "b-vm stopped\n",
				"c.local": "c-vm running\n",
			},
			wantOut:   []string{"a\ta-vm running", "b\tb-vm stopped", "c\tc-vm running"},
			wantCalls: 3,
		},
		{
			name: "least loaded placement scores hosts",
			args: []string{"run", "--policy=least-loaded", "-linux"},
			outputs: map[string]string{
				"a.local": "a1 running\na2 running\n",
				"b.local": "b1 running\n",
				"c.local": "",
			},
			wantOut:   []string{"selected c", "a=2", "b=1", "c=0"},
			wantCalls: 3,
		},
		{
			name:      "all hosts down fails placement",
			args:      []string{"run", "--policy=least-loaded"},
			errs:      map[string]error{"a.local": errors.New("down"), "b.local": errors.New("down"), "c.local": errors.New("down")},
			wantErr:   "all remotes unreachable",
			wantCalls: 3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeFleetHostsConfig(t, "a", "b", "c")
			runner := &fakeFleetRunner{outputs: tt.outputs, errs: tt.errs}
			var out bytes.Buffer
			err := runFleetCommandWithRunner(context.Background(), tt.args, path, runner, &out, &bytes.Buffer{})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want substring %q", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatalf("fleet command: %v", err)
			}
			for _, want := range tt.wantOut {
				if !strings.Contains(out.String(), want) {
					t.Fatalf("output missing %q:\n%s", want, out.String())
				}
			}
			runner.assertCallsWithArgs(t, []string{"vm", "list"}, tt.wantCalls)
			if tt.name == "least loaded placement scores hosts" {
				runner.assertSawCall(t, "c.local", []string{"run", "-linux"})
			}
		})
	}
}

func TestFleetImageTransferCommands(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		wantOut string
		want    []fakeFleetCommandCall
	}{
		{
			name:    "push local to remote",
			args:    []string{"image", "push", "base:latest", "a"},
			wantOut: "pushed image base:latest to a",
			want: []fakeFleetCommandCall{
				{host: "", args: []string{"image", "push", "base:latest", "-"}},
				{host: "a.local", args: []string{"image", "load", "-"}},
			},
		},
		{
			name:    "pull remote to local",
			args:    []string{"image", "pull", "base:latest", "a"},
			wantOut: "pulled image base:latest from a",
			want: []fakeFleetCommandCall{
				{host: "a.local", args: []string{"image", "push", "base:latest", "-"}},
				{host: "", args: []string{"image", "load", "-"}},
			},
		},
		{
			name:    "sync remote to remote",
			args:    []string{"image", "sync", "base:latest", "a", "b"},
			wantOut: "synced image base:latest from a to b",
			want: []fakeFleetCommandCall{
				{host: "a.local", args: []string{"image", "push", "base:latest", "-"}},
				{host: "b.local", args: []string{"image", "load", "-"}},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeFleetTestConfig(t)
			runner := &fakeFleetRunner{streamPayload: "tarball"}
			var out bytes.Buffer
			if err := runFleetCommandWithRunner(context.Background(), tc.args, path, runner, &out, &bytes.Buffer{}); err != nil {
				t.Fatalf("runFleetCommandWithRunner: %v", err)
			}
			if !strings.Contains(out.String(), tc.wantOut) {
				t.Fatalf("output = %q, want %q", out.String(), tc.wantOut)
			}
			runner.assertCommandCalls(t, tc.want)
			if runner.loaded != "tarball" {
				t.Fatalf("loaded = %q, want tarball", runner.loaded)
			}
		})
	}
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

func writeFleetHostsConfig(t *testing.T, names ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fleet.json")
	cfg := &fleetpkg.Config{}
	for _, name := range names {
		if err := cfg.Add(name, fleetpkg.Remote{Host: name + ".local"}); err != nil {
			t.Fatalf("Add %s: %v", name, err)
		}
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

type fakeFleetCommandCall struct {
	host string
	args []string
}

type fakeFleetRunner struct {
	mu            sync.Mutex
	remote        fleetpkg.Remote
	args          []string
	outputs       map[string]string
	errs          map[string]error
	calls         []fakeFleetCall
	commandCalls  []fakeFleetCommandCall
	streamPayload string
	loaded        string
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

func (f *fakeFleetRunner) RunCommand(ctx context.Context, remote fleetpkg.Remote, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	f.mu.Lock()
	f.commandCalls = append(f.commandCalls, fakeFleetCommandCall{host: remote.Host, args: append([]string(nil), args...)})
	payload := f.streamPayload
	err := error(nil)
	if f.errs != nil {
		err = f.errs[remote.Host]
	}
	f.mu.Unlock()
	if err != nil {
		return err
	}
	if len(args) >= 2 && args[0] == "image" && args[1] == "push" {
		_, err := io.WriteString(stdout, payload)
		return err
	}
	if len(args) >= 2 && args[0] == "image" && args[1] == "load" {
		var b bytes.Buffer
		if _, err := io.Copy(&b, stdin); err != nil {
			return err
		}
		f.mu.Lock()
		f.loaded = b.String()
		f.mu.Unlock()
	}
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

func (f *fakeFleetRunner) assertCallsWithArgs(t *testing.T, wantArgs []string, wantCount int) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, call := range f.calls {
		if reflect.DeepEqual(call.args, wantArgs) {
			count++
		}
	}
	if count != wantCount {
		t.Fatalf("calls = %#v, count for %#v = %d, want %d", f.calls, wantArgs, count, wantCount)
	}
}

func (f *fakeFleetRunner) assertSawCall(t *testing.T, wantHost string, wantArgs []string) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, call := range f.calls {
		if call.remote.Host == wantHost && reflect.DeepEqual(call.args, wantArgs) {
			return
		}
	}
	t.Fatalf("calls = %#v, missing host=%q args=%#v", f.calls, wantHost, wantArgs)
}

func (f *fakeFleetRunner) assertCommandCalls(t *testing.T, want []fakeFleetCommandCall) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.commandCalls) != len(want) {
		t.Fatalf("command calls = %#v, want %#v", f.commandCalls, want)
	}
	for _, w := range want {
		found := false
		for _, got := range f.commandCalls {
			if got.host == w.host && reflect.DeepEqual(got.args, w.args) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("command calls = %#v, missing %#v", f.commandCalls, w)
		}
	}
}
