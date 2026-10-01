package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tmc/cove/internal/runs"
	pb "github.com/tmc/cove/proto/agentpb"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

func workspaceTestOptions(t *testing.T) workspaceOptions {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	return workspaceOptions{VM: "test-workspace", OS: "darwin", Source: source, Output: filepath.Join(dir, "output"), SourceMode: "ro", Retain: "retain", Timeout: time.Minute, MinFreeGiB: 1, Args: []string{"go", "test", "./..."}}
}

func TestWorkspacePlan(t *testing.T) {
	o := workspaceTestOptions(t)
	o.Prepare = "golang"
	p, err := planGoWorkspace(o)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Source.ReadOnly || p.Output.ReadOnly || p.GuestRoute != "user" || len(p.Preparation.Recipes) == 0 {
		t.Fatalf("plan = %+v", p)
	}
	if _, err := os.Stat(o.Output); !os.IsNotExist(err) {
		t.Fatal("planning created output")
	}
	if strings.Contains(p.SourceGuestPath, "source") && p.SourceGuestPath != "/Volumes/My Shared Files/cove-workspace-source" {
		t.Fatalf("wrong guest source path: %q", p.SourceGuestPath)
	}
	o.SourceMode = "rw"
	p, err = planGoWorkspace(o)
	if err != nil || p.Source.ReadOnly {
		t.Fatalf("explicit rw = %+v, %v", p, err)
	}
}

func TestWorkspacePlanRefusals(t *testing.T) {
	tests := []struct {
		name   string
		change func(*workspaceOptions)
	}{
		{"missing-vm", func(o *workspaceOptions) { o.VM = "" }},
		{"unsafe-vm", func(o *workspaceOptions) { o.VM = "../outside" }},
		{"same-base", func(o *workspaceOptions) { o.From = o.VM }},
		{"invalid-os", func(o *workspaceOptions) { o.OS = "windows" }},
		{"invalid-mode", func(o *workspaceOptions) { o.SourceMode = "write" }},
		{"overlapping-output", func(o *workspaceOptions) { o.Output = filepath.Join(o.Source, "output") }},
		{"missing-output", func(o *workspaceOptions) { o.Output = "" }},
		{"unsafe-discard", func(o *workspaceOptions) { o.Retain = "discard-success" }},
		{"empty-task", func(o *workspaceOptions) { o.Args = nil }},
		{"zero-budget", func(o *workspaceOptions) { o.MinFreeGiB = 0 }},
		{"unknown-preparation", func(o *workspaceOptions) { o.Prepare = "unknown-recipe" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := workspaceTestOptions(t)
			tt.change(&o)
			if _, err := planGoWorkspace(o); err == nil {
				t.Fatal("invalid profile accepted")
			}
		})
	}
}

func workspaceTestDeps(t *testing.T, steps *[]string) workspaceDeps {
	t.Helper()
	dir := t.TempDir()
	ok := func(name string) error { *steps = append(*steps, name); return nil }
	return workspaceDeps{
		Resolve: func(workspacePlan) (string, bool, error) { return dir, true, ok("resolve") },
		Fork:    func(string, string) error { return ok("fork") }, HostStorage: func(workspacePlan) error { return ok("host-storage") }, ConfigureShares: func(workspacePlan, string) error { return ok("shares") }, Start: func(workspacePlan, string) error { return ok("start") },
		Ready: func(context.Context, workspacePlan, string) ([]byte, error) {
			return []byte(`{"root":true,"user":true}`), ok("ready")
		}, GuestStorage: func(workspacePlan, string) error { return ok("guest-storage") },
		GoCheck: func(context.Context, workspacePlan, string) (string, error) {
			return "go version go1.26 darwin/arm64", ok("go")
		}, Prepare: func(context.Context, workspacePlan, string) error { return ok("prepare") }, Mount: func(context.Context, workspacePlan, string) error { return ok("mount") },
		Task: func(context.Context, workspaceOptions, workspacePlan, string, commandEnv) (*controlpb.AgentExecResponse, error) {
			return &controlpb.AgentExecResponse{Stdout: "PASS\n"}, ok("task")
		}, Capture: func(context.Context, workspacePlan, string) ([]byte, error) {
			return []byte(`{"retained":true}`), ok("capture")
		}, Discard: func(workspacePlan, string) error { return ok("discard") }, RunsRoot: filepath.Join(t.TempDir(), "runs"),
	}
}

func TestWorkspaceRunReceipt(t *testing.T) {
	o := workspaceTestOptions(t)
	p, err := planGoWorkspace(o)
	if err != nil {
		t.Fatal(err)
	}
	var steps []string
	d := workspaceTestDeps(t, &steps)
	receipt, err := openGoWorkspace(context.Background(), o, p, d, commandEnv{Stdout: io.Discard, Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Outcome != "success" || receipt.Disposition != "retained" {
		t.Fatalf("receipt = %+v", receipt)
	}
	r, err := runs.LoadRecord(receipt.Bundle)
	if err != nil {
		t.Fatal(err)
	}
	if r.Manifest.Task == nil || r.Manifest.Task.GuestRoute != "user" || r.Manifest.Outcome != "success" || len(r.Manifest.Artifacts) < 4 {
		t.Fatalf("run = %+v", r.Manifest)
	}
	if r.Manifest.Task.ImageDigest != "" {
		t.Fatal("unverified image digest invented")
	}
	if !reflect.DeepEqual(steps, []string{"resolve", "host-storage", "shares", "start", "ready", "guest-storage", "go", "mount", "task"}) {
		t.Fatalf("steps = %v", steps)
	}
}

func TestWorkspaceFailedTaskNoRetry(t *testing.T) {
	o := workspaceTestOptions(t)
	p, _ := planGoWorkspace(o)
	var steps []string
	d := workspaceTestDeps(t, &steps)
	calls := 0
	d.Task = func(context.Context, workspaceOptions, workspacePlan, string, commandEnv) (*controlpb.AgentExecResponse, error) {
		calls++
		return nil, errors.New("unknown-result transport error")
	}
	d.Capture = func(context.Context, workspacePlan, string) ([]byte, error) { return nil, errors.New("capture failed") }
	receipt, err := openGoWorkspace(context.Background(), o, p, d, commandEnv{Stdout: io.Discard})
	if err == nil || calls != 1 || receipt.Disposition != "retained" || receipt.FailedStep != "task" {
		t.Fatalf("receipt %+v, error %v, calls %d", receipt, err, calls)
	}
	r, e := runs.LoadRecord(receipt.Bundle)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(r.Manifest.PrimaryError, "outcome unknown") || strings.Contains(r.Manifest.PrimaryError, "capture failed") || len(r.Manifest.CaptureErrors) != 1 {
		t.Fatalf("primary/capture confused: %+v", r.Manifest)
	}
}

func TestWorkspacePrerequisiteStopsTask(t *testing.T) {
	for _, name := range []string{"readiness", "storage", "mount"} {
		t.Run(name, func(t *testing.T) {
			o := workspaceTestOptions(t)
			p, _ := planGoWorkspace(o)
			var steps []string
			d := workspaceTestDeps(t, &steps)
			failure := errors.New("precondition failed")
			switch name {
			case "readiness":
				d.Ready = func(context.Context, workspacePlan, string) ([]byte, error) { return nil, failure }
			case "storage":
				d.GuestStorage = func(workspacePlan, string) error { return failure }
			case "mount":
				d.Mount = func(context.Context, workspacePlan, string) error { return failure }
			}
			receipt, err := openGoWorkspace(context.Background(), o, p, d, commandEnv{Stdout: io.Discard})
			if !errors.Is(err, failure) || receipt.Outcome != "prerequisite_failure" {
				t.Fatalf("receipt %+v, error %v", receipt, err)
			}
			for _, step := range steps {
				if step == "task" {
					t.Fatal("task ran after prerequisite failure")
				}
			}
		})
	}
}

func TestWorkspaceCheckedPreparation(t *testing.T) {
	o := workspaceTestOptions(t)
	o.Prepare = "golang"
	p, _ := planGoWorkspace(o)
	var steps []string
	d := workspaceTestDeps(t, &steps)
	checks := 0
	prepares := 0
	d.GoCheck = func(context.Context, workspacePlan, string) (string, error) {
		checks++
		if checks == 1 {
			return "", errors.New("missing Go")
		}
		return "go version go1.26 darwin/arm64", nil
	}
	d.Prepare = func(context.Context, workspacePlan, string) error { prepares++; return nil }
	if _, err := openGoWorkspace(context.Background(), o, p, d, commandEnv{Stdout: io.Discard}); err != nil {
		t.Fatal(err)
	}
	if checks != 2 || prepares != 1 {
		t.Fatalf("checks %d prepares %d", checks, prepares)
	}
	checks = 0
	prepares = 0
	d.GoCheck = func(context.Context, workspacePlan, string) (string, error) {
		checks++
		return "go version go1.26 darwin/arm64", nil
	}
	if _, err := openGoWorkspace(context.Background(), o, p, d, commandEnv{Stdout: io.Discard}); err != nil {
		t.Fatal(err)
	}
	if checks != 1 || prepares != 0 {
		t.Fatalf("checks %d prepares %d", checks, prepares)
	}
}

func TestWorkspaceFreshForkRetention(t *testing.T) {
	o := workspaceTestOptions(t)
	o.From = "prepared-base"
	o.Retain = "discard-success"
	p, _ := planGoWorkspace(o)
	var steps []string
	d := workspaceTestDeps(t, &steps)
	resolves := 0
	d.Resolve = func(workspacePlan) (string, bool, error) { resolves++; return "/guest/owned", resolves > 1, nil }
	receipt, err := openGoWorkspace(context.Background(), o, p, d, commandEnv{Stdout: io.Discard})
	if err != nil || receipt.Disposition != "discarded" {
		t.Fatalf("receipt %+v, error %v", receipt, err)
	}
	resolves = 0
	d.Discard = func(workspacePlan, string) error { return errors.New("stop failed") }
	receipt, err = openGoWorkspace(context.Background(), o, p, d, commandEnv{Stdout: io.Discard})
	if err == nil || receipt.Outcome != "cleanup_incomplete" || receipt.Disposition != "retained" {
		t.Fatalf("receipt %+v, error %v", receipt, err)
	}
	d.Resolve = func(workspacePlan) (string, bool, error) { return "/guest/existing", true, nil }
	receipt, err = openGoWorkspace(context.Background(), o, p, d, commandEnv{Stdout: io.Discard})
	if err == nil || receipt.FailedStep != "resolve" {
		t.Fatalf("existing guest replaced: %+v, %v", receipt, err)
	}
}

func TestWorkspaceNeverReadyRetainsOwnedGuest(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		want string
	}{
		{"unavailable", errors.New("agent unavailable"), "prerequisite_failure"},
		{"timeout", context.DeadlineExceeded, "timed_out"},
		{"canceled", context.Canceled, "canceled"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			o := workspaceTestOptions(t)
			o.From = "prepared-base"
			o.Retain = "discard-success"
			p, err := planGoWorkspace(o)
			if err != nil {
				t.Fatal(err)
			}
			var steps []string
			d := workspaceTestDeps(t, &steps)
			resolves := 0
			d.Resolve = func(workspacePlan) (string, bool, error) {
				resolves++
				return "/guest/owned", resolves > 1, nil
			}
			d.Ready = func(context.Context, workspacePlan, string) ([]byte, error) { return nil, tt.err }
			d.Capture = func(context.Context, workspacePlan, string) ([]byte, error) {
				return nil, errors.New("agent capture unavailable")
			}
			receipt, err := openGoWorkspace(context.Background(), o, p, d, commandEnv{Stdout: io.Discard})
			if !errors.Is(err, tt.err) || receipt.FailedStep != "readiness" || receipt.Outcome != tt.want || receipt.Disposition != "retained" || receipt.GuestDirectory != "/guest/owned" {
				t.Fatalf("receipt %+v, error %v", receipt, err)
			}
			for _, step := range steps {
				if step == "task" || step == "discard" {
					t.Fatalf("%s ran for a guest that never became ready", step)
				}
			}
		})
	}
}

func TestWorkspaceArgvNoShellInjection(t *testing.T) {
	target := filepath.Join(t.TempDir(), "must-not-exist")
	arg := "$(touch " + target + "); quoted ' value"
	args := workspaceUserArgs([]string{"/usr/bin/printf", "%s", arg})
	out, err := exec.Command(args[0], args[1:]...).Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != arg {
		t.Fatalf("argument changed: %q", out)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("argument executed")
	}
}

func TestWorkspaceParseAndPlanHidesArguments(t *testing.T) {
	o := workspaceTestOptions(t)
	var out bytes.Buffer
	opts, _, err := parseWorkspaceOptions([]string{"-vm", o.VM, "-source", o.Source, "-output", o.Output, "--", "go", "test", "secret-argument"}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(opts.Args, []string{"go", "test", "secret-argument"}) {
		t.Fatalf("args = %v", opts.Args)
	}
	p, err := planGoWorkspace(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeWorkspacePlan(&out, p, true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "secret-argument") {
		t.Fatal("plan disclosed task arguments")
	}
}

func TestWorkspaceTaskEnvironmentWorksWithoutAgentEnv(t *testing.T) {
	p := workspacePlan{OutputGuestPath: filepath.Join(t.TempDir(), "path with spaces ; inert")}
	args := workspaceTaskArgs(p, []string{"/bin/sh", "-c", `printf '%s\n%s\n%s\n' "$GOCACHE" "$GOMODCACHE" "$GOTMPDIR"`})
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{filepath.Join(p.OutputGuestPath, "gocache"), filepath.Join(p.OutputGuestPath, "gomodcache"), filepath.Join(p.OutputGuestPath, "tmp"), ""}, "\n")
	if string(out) != want {
		t.Fatalf("actual task env = %q, want %q", out, want)
	}
}

func TestWorkspaceShareConflict(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(t.TempDir(), "source")
	output := filepath.Join(t.TempDir(), "output")
	before := []SharedFolderEntry{{Path: source, Tag: workspaceSourceTag, ReadOnly: false}}
	if err := saveSharedFolders(dir, before); err != nil {
		t.Fatal(err)
	}
	p := workspacePlan{Source: vzscriptPlannedMount{Path: source, ReadOnly: true}, Output: vzscriptPlannedMount{Path: output}}
	if err := configureWorkspaceShares(p, dir); err == nil {
		t.Fatal("saved rw silently reused for declared ro")
	}
	if got := LoadSharedFolders(dir); !reflect.DeepEqual(got, before) {
		t.Fatalf("conflict mutated shares: %+v", got)
	}
}

func TestWorkspaceSourceProvenance(t *testing.T) {
	source := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", source}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.name", "Workspace Test")
	run("config", "user.email", "workspace@example.invalid")
	if err := os.WriteFile(filepath.Join(source, "source.go"), []byte("package example\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run("add", "source.go")
	run("commit", "-qm", "initial source")
	before := workspaceSourceProvenance(context.Background(), source)
	if !bytes.Contains(before, []byte(`"dirty":false`)) || !bytes.Contains(before, []byte(`"identity":"git"`)) {
		t.Fatalf("source provenance %s", before)
	}
	if err := os.WriteFile(filepath.Join(source, "private-secret-file"), []byte("private-value"), 0600); err != nil {
		t.Fatal(err)
	}
	after := workspaceSourceProvenance(context.Background(), source)
	if !bytes.Contains(after, []byte(`"dirty":true`)) || bytes.Contains(after, []byte("private-secret-file")) || bytes.Contains(after, []byte("private-value")) {
		t.Fatalf("unsafe/inaccurate provenance %s", after)
	}
}

func TestWorkspaceCancellationBeforeDispatch(t *testing.T) {
	o := workspaceTestOptions(t)
	p, _ := planGoWorkspace(o)
	var steps []string
	d := workspaceTestDeps(t, &steps)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	receipt, err := openGoWorkspace(ctx, o, p, d, commandEnv{Stdout: io.Discard, Stderr: io.Discard})
	if !errors.Is(err, context.Canceled) || receipt.Outcome != "canceled" || len(steps) != 0 {
		t.Fatalf("receipt %+v, error %v, steps %v", receipt, err, steps)
	}
}

func TestWorkspacePlanFreezesPreparation(t *testing.T) {
	o := workspaceTestOptions(t)
	name := filepath.Join(t.TempDir(), "prepare.vzscript")
	initial := []byte("# guest-os: darwin\necho 'original'\n")
	if err := os.WriteFile(name, initial, 0600); err != nil {
		t.Fatal(err)
	}
	o.Prepare = name
	p, err := planGoWorkspace(o)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte("# guest-os: windows\nunknown-command\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(p.recipeData[name], initial) {
		t.Fatal("planned preparation source changed after dispatch plan")
	}
	var out bytes.Buffer
	if err := writeWorkspacePlan(&out, p, true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "original") {
		t.Fatal("private frozen command arguments appeared in plan")
	}
}

func workspaceStreamFixture(t *testing.T, serve func(net.Conn, *controlpb.ControlRequest)) string {
	t.Helper()
	root := filepath.Join(os.Getenv("HOME"), "tmp")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "ws-stream-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		scanner := bufio.NewScanner(conn)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		if !scanner.Scan() {
			return
		}
		var req controlpb.ControlRequest
		if err := protojsonUnmarshaler.Unmarshal(scanner.Bytes(), &req); err != nil {
			t.Errorf("request decode: %v", err)
			return
		}
		serve(conn, &req)
	}()
	return socket
}

func workspaceStreamEvent(conn net.Conn, event any) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return writeResponse(conn, &controlpb.ControlResponse{Success: true, Data: string(data)})
}

func TestWorkspaceStreamEarlyOutputAndExit(t *testing.T) {
	delivered := make(chan struct{}, 1)
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	finished := make(chan struct{})
	socket := workspaceStreamFixture(t, func(conn net.Conn, req *controlpb.ControlRequest) {
		if req.Type != "agent-user-exec-stream" || req.GetAgentExec().WorkingDir != "/source" || !reflect.DeepEqual(req.GetAgentExec().Args, []string{"go", "test", "literal ; arg"}) {
			t.Errorf("wrong streamed argv/route/workdir: %s", req.Type)
		}
		_ = workspaceStreamEvent(conn, map[string]any{"stream": "stdout", "data": base64.StdEncoding.EncodeToString([]byte("before done\n"))})
		<-release
		_ = workspaceStreamEvent(conn, map[string]any{"done": true, "exitCode": 9})
	})
	var result *controlpb.AgentExecResponse
	var err error
	writer := workspaceNotifyWriter{notify: delivered}
	go func() {
		result, err = streamWorkspaceTask(context.Background(), socket, []string{"go", "test", "literal ; arg"}, "/source", time.Second, writer, io.Discard)
		close(finished)
	}()
	select {
	case <-delivered:
	case <-time.After(time.Second):
		t.Fatal("output wasn't streamed before final response")
	}
	select {
	case <-finished:
		t.Fatal("stream completed without final status")
	default:
	}
	close(release)
	<-finished
	if err != nil || result.ExitCode != 9 || result.Stdout != "before done\n" {
		t.Fatalf("stream result %+v, %v", result, err)
	}
}

type workspaceNotifyWriter struct{ notify chan struct{} }

func (w workspaceNotifyWriter) Write(p []byte) (int, error) {
	select {
	case w.notify <- struct{}{}:
	default:
	}
	return len(p), nil
}

func TestWorkspaceStreamRequiresFinalStatus(t *testing.T) {
	for _, name := range []string{"eof", "missing-status"} {
		t.Run(name, func(t *testing.T) {
			socket := workspaceStreamFixture(t, func(conn net.Conn, _ *controlpb.ControlRequest) {
				_ = workspaceStreamEvent(conn, map[string]any{"stream": "stderr", "data": base64.StdEncoding.EncodeToString([]byte("partial evidence"))})
				if name == "missing-status" {
					_ = workspaceStreamEvent(conn, map[string]any{"done": true})
				}
			})
			result, err := streamWorkspaceTask(context.Background(), socket, []string{"go", "test"}, "", time.Second, io.Discard, io.Discard)
			if err == nil || result.Stderr != "partial evidence" {
				t.Fatalf("missing final accepted or partial evidence lost: %+v, %v", result, err)
			}
		})
	}
}

func TestWorkspaceStreamQuietDeadline(t *testing.T) {
	closed := make(chan struct{})
	socket := workspaceStreamFixture(t, func(conn net.Conn, _ *controlpb.ControlRequest) { _, _ = io.Copy(io.Discard, conn); close(closed) })
	start := time.Now()
	_, err := streamWorkspaceTask(context.Background(), socket, []string{"quiet"}, "", 100*time.Millisecond, io.Discard, io.Discard)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("quiet stream error %v, duration %s", err, time.Since(start))
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("cancel didn't close task observation socket")
	}
}

func TestWorkspaceStreamCaptureBound(t *testing.T) {
	socket := workspaceStreamFixture(t, func(conn net.Conn, _ *controlpb.ControlRequest) {
		chunk := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("x"), 64<<10))
		for i := 0; i < 40; i++ {
			if err := workspaceStreamEvent(conn, map[string]any{"stream": "stdout", "data": chunk}); err != nil {
				return
			}
		}
		_ = workspaceStreamEvent(conn, map[string]any{"done": true, "exitCode": 0})
	})
	result, err := streamWorkspaceTask(context.Background(), socket, []string{"go", "test"}, "", time.Second, io.Discard, io.Discard)
	if err != nil || len(result.Stdout) != (2<<20)+1 {
		t.Fatalf("capture size %d, error %v", len(result.Stdout), err)
	}
}

func TestWorkspacePartialStreamFailureReceipt(t *testing.T) {
	o := workspaceTestOptions(t)
	p, _ := planGoWorkspace(o)
	var steps []string
	d := workspaceTestDeps(t, &steps)
	d.Task = func(context.Context, workspaceOptions, workspacePlan, string, commandEnv) (*controlpb.AgentExecResponse, error) {
		return &controlpb.AgentExecResponse{Stdout: "partial output"}, io.EOF
	}
	receipt, err := openGoWorkspace(context.Background(), o, p, d, commandEnv{Stdout: io.Discard, Stderr: io.Discard})
	if err == nil || receipt.Outcome != "task_failure" || receipt.Disposition != "retained" {
		t.Fatalf("receipt %+v, error %v", receipt, err)
	}
	data, e := os.ReadFile(filepath.Join(receipt.Bundle, "artifacts", "task-result.json"))
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Contains(data, []byte(`"final_result_received":false`)) || bytes.Contains(data, []byte(`"exit_code"`)) {
		t.Fatalf("unknown result manufactured exit status: %s", data)
	}
	data, e = os.ReadFile(filepath.Join(receipt.Bundle, "artifacts", "task-stdout.log"))
	if e != nil || string(data) != "partial output" {
		t.Fatalf("partial evidence %q, %v", data, e)
	}
}

func TestWorkspaceServerStreamExitStatus(t *testing.T) {
	for _, name := range []string{"missing", "success", "nonzero"} {
		t.Run(name, func(t *testing.T) {
			stream := &fakeExecStream{frames: make(chan *pb.ExecOutput, 4)}
			stream.queue(&pb.ExecOutput{Data: []byte("guest evidence")})
			if name != "missing" {
				code := int32(0)
				if name == "nonzero" {
					code = 17
				}
				stream.queue(&pb.ExecOutput{ExitCode: &code})
			}
			stream.close()
			socket := workspaceStreamFixture(t, func(conn net.Conn, _ *controlpb.ControlRequest) { forwardAgentExecStream(conn, stream) })
			result, err := streamWorkspaceTask(context.Background(), socket, []string{"go", "test"}, "", time.Second, io.Discard, io.Discard)
			if name == "missing" {
				if err == nil {
					t.Fatal("guest EOF without exit event became server success")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if name == "nonzero" && result.ExitCode != 17 {
					t.Fatalf("exit code = %d", result.ExitCode)
				}
			}
			if result.Stdout != "guest evidence" {
				t.Fatalf("guest evidence lost: %+v", result)
			}
		})
	}
}
