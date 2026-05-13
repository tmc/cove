package action

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	outputs map[string]Output
	errors  map[string]error
	calls   []string
}

func (r *fakeRunner) Run(_ context.Context, name string, args ...string) (Output, error) {
	call := strings.Join(append([]string{name}, args...), "\x00")
	r.calls = append(r.calls, call)
	if err := r.errors[call]; err != nil {
		return r.outputs[call], err
	}
	return r.outputs[call], nil
}

func key(name string, args ...string) string {
	return strings.Join(append([]string{name}, args...), "\x00")
}

func TestCheckNetworkFallsBackToTopLevelNetwork(t *testing.T) {
	r := &fakeRunner{
		outputs: map[string]Output{
			key("cove", "ctl", "network", "list"): {Stderr: "unknown command"},
			key("cove", "network", "list"):        {Stdout: "Available network interfaces\n"},
		},
		errors: map[string]error{
			key("cove", "ctl", "network", "list"): errors.New("exit status 1"),
		},
	}
	got := checkNetwork(context.Background(), DoctorConfig{CoveBin: "cove", Runner: r})
	if got.Status != StatusPass {
		t.Fatalf("status = %q, want pass: %s", got.Status, got.Message)
	}
	if len(r.calls) != 2 {
		t.Fatalf("calls = %v, want ctl probe then fallback", r.calls)
	}
}

func TestWriteReport(t *testing.T) {
	report := Report{
		Status: StatusWarn,
		Checks: []CheckResult{
			{Name: "disk-capacity", Status: StatusWarn, Message: "low"},
		},
	}
	var text bytes.Buffer
	if err := WriteReport(&text, report, false); err != nil {
		t.Fatalf("WriteReport text: %v", err)
	}
	if !strings.Contains(text.String(), "[warn] disk-capacity: low") {
		t.Fatalf("text output = %q", text.String())
	}
	var js bytes.Buffer
	if err := WriteReport(&js, report, true); err != nil {
		t.Fatalf("WriteReport json: %v", err)
	}
	if !strings.Contains(js.String(), `"status": "warn"`) {
		t.Fatalf("json output = %q", js.String())
	}
}

func TestRunPreparePass(t *testing.T) {
	r := &fakeRunner{outputs: map[string]Output{}, errors: map[string]error{}}
	for _, call := range []string{
		key("cove", "shell", "ubuntu:ci", "--", "which", "bash"),
		key("cove", "shell", "ubuntu:ci", "--", "which", "curl"),
		key("cove", "shell", "ubuntu:ci", "--", "which", "git"),
		key("cove", "shell", "ubuntu:ci", "--", "which", "docker"),
		key("cove", "vm", "tree", "--reachable-from", "ubuntu:ci"),
	} {
		r.outputs[call] = Output{Stdout: "ok\n"}
	}
	r.outputs[key("cove", "shell", "ubuntu:ci", "--", "vz-agent", "-version")] = Output{Stdout: "vz-agent abc123\n"}
	r.outputs[key("cove", "version")] = Output{Stdout: "cove abc123 (commit abc123, built now)\n"}
	r.outputs[key("cove", "shell", "ubuntu:ci", "--", "echo", "OK")] = Output{Stdout: "OK\n"}
	r.outputs[key("cove", "image", "inspect", "-json", "ubuntu:ci")] = Output{Stdout: `{"built_at":"2026-05-05T10:00:00Z"}`}

	got := RunPrepare(context.Background(), PrepareConfig{CoveBin: "cove", Ref: "ubuntu:ci", Force: true, Runner: r})
	if got.Status != StatusPass || got.ExitCode() != 0 {
		t.Fatalf("RunPrepare status = %q exit = %d, want pass/0", got.Status, got.ExitCode())
	}
	for _, want := range []string{
		key("cove", "image", "inspect", "-json", "ubuntu:ci"),
		key("cove", "shell", "ubuntu:ci", "--", "which", "docker"),
		key("cove", "vm", "tree", "--reachable-from", "ubuntu:ci"),
	} {
		if !contains(r.calls, want) {
			t.Fatalf("missing call %q in %v", want, r.calls)
		}
	}
}

func TestRunPrepareFailsMissingDependency(t *testing.T) {
	r := &fakeRunner{outputs: map[string]Output{}, errors: map[string]error{}}
	r.outputs[key("cove", "shell", "ubuntu:ci", "--", "vz-agent", "-version")] = Output{Stdout: "vz-agent abc123\n"}
	r.outputs[key("cove", "version")] = Output{Stdout: "cove abc123 (commit abc123, built now)\n"}
	r.errors[key("cove", "shell", "ubuntu:ci", "--", "which", "docker")] = errors.New("exit 1")
	r.outputs[key("cove", "shell", "ubuntu:ci", "--", "which", "docker")] = Output{Stderr: "docker not found"}
	r.outputs[key("cove", "shell", "ubuntu:ci", "--", "echo", "OK")] = Output{Stdout: "OK\n"}
	got := RunPrepare(context.Background(), PrepareConfig{CoveBin: "cove", Ref: "ubuntu:ci", Force: true, Runner: r})
	if got.Status != StatusFail || got.ExitCode() != 1 {
		t.Fatalf("RunPrepare status = %q exit = %d, want fail/1", got.Status, got.ExitCode())
	}
}

func TestRunPrepareFailsStaleAgent(t *testing.T) {
	r := &fakeRunner{outputs: map[string]Output{}, errors: map[string]error{}}
	r.outputs[key("cove", "shell", "ubuntu:ci", "--", "vz-agent", "-version")] = Output{Stdout: "vz-agent old\n"}
	r.outputs[key("cove", "version")] = Output{Stdout: "cove new (commit new, built now)\n"}
	r.outputs[key("cove", "shell", "ubuntu:ci", "--", "echo", "OK")] = Output{Stdout: "OK\n"}
	got := RunPrepare(context.Background(), PrepareConfig{CoveBin: "cove", Ref: "ubuntu:ci", Force: true, Runner: r})
	if got.Status != StatusFail {
		t.Fatalf("RunPrepare status = %q, want fail", got.Status)
	}
}

func TestRunPrepareSkipsFreshImage(t *testing.T) {
	r := &fakeRunner{
		outputs: map[string]Output{
			key("cove", "image", "inspect", "-json", "ubuntu:ci"): {Stdout: `{"built_at":"2026-05-05T10:00:00Z"}`},
		},
		errors: map[string]error{},
	}
	got := RunPrepare(context.Background(), PrepareConfig{
		CoveBin: "cove",
		Ref:     "ubuntu:ci",
		Runner:  r,
		TTL:     24 * time.Hour,
		Now:     mustTime(t, "2026-05-05T12:00:00Z"),
	})
	if got.Status != StatusPass {
		t.Fatalf("RunPrepare status = %q, want pass", got.Status)
	}
	if len(got.Checks) != 1 || got.Checks[0].Name != "image-fresh" {
		t.Fatalf("checks = %#v, want image-fresh only", got.Checks)
	}
	if !strings.Contains(got.Checks[0].Message, "image already prepared") {
		t.Fatalf("fresh message = %q", got.Checks[0].Message)
	}
	if len(r.calls) != 1 {
		t.Fatalf("calls = %v, want inspect only", r.calls)
	}
}

func TestRunPrepareChecksStaleImage(t *testing.T) {
	r := &fakeRunner{outputs: map[string]Output{}, errors: map[string]error{}}
	for _, call := range []string{
		key("cove", "image", "inspect", "-json", "ubuntu:ci"),
		key("cove", "shell", "ubuntu:ci", "--", "which", "bash"),
		key("cove", "shell", "ubuntu:ci", "--", "which", "curl"),
		key("cove", "shell", "ubuntu:ci", "--", "which", "git"),
		key("cove", "shell", "ubuntu:ci", "--", "which", "docker"),
		key("cove", "vm", "tree", "--reachable-from", "ubuntu:ci"),
	} {
		r.outputs[call] = Output{Stdout: "ok\n"}
	}
	r.outputs[key("cove", "shell", "ubuntu:ci", "--", "vz-agent", "-version")] = Output{Stdout: "vz-agent abc123\n"}
	r.outputs[key("cove", "version")] = Output{Stdout: "cove abc123 (commit abc123, built now)\n"}
	r.outputs[key("cove", "shell", "ubuntu:ci", "--", "echo", "OK")] = Output{Stdout: "OK\n"}
	r.outputs[key("cove", "image", "inspect", "-json", "ubuntu:ci")] = Output{Stdout: `{"built_at":"2026-05-01T10:00:00Z"}`}

	got := RunPrepare(context.Background(), PrepareConfig{CoveBin: "cove", Ref: "ubuntu:ci", Runner: r, TTL: 24 * time.Hour, Now: mustTime(t, "2026-05-05T12:00:00Z")})
	if got.Status != StatusPass {
		t.Fatalf("RunPrepare status = %q, want pass", got.Status)
	}
	if !contains(r.calls, key("cove", "shell", "ubuntu:ci", "--", "which", "docker")) {
		t.Fatalf("stale image did not run full checks: %v", r.calls)
	}
}

func TestRunPrepareForceBypassesFreshSkip(t *testing.T) {
	r := &fakeRunner{outputs: map[string]Output{}, errors: map[string]error{}}
	for _, call := range []string{
		key("cove", "image", "inspect", "-json", "ubuntu:ci"),
		key("cove", "shell", "ubuntu:ci", "--", "which", "bash"),
		key("cove", "shell", "ubuntu:ci", "--", "which", "curl"),
		key("cove", "shell", "ubuntu:ci", "--", "which", "git"),
		key("cove", "shell", "ubuntu:ci", "--", "which", "docker"),
		key("cove", "vm", "tree", "--reachable-from", "ubuntu:ci"),
	} {
		r.outputs[call] = Output{Stdout: "ok\n"}
	}
	r.outputs[key("cove", "shell", "ubuntu:ci", "--", "vz-agent", "-version")] = Output{Stdout: "vz-agent abc123\n"}
	r.outputs[key("cove", "version")] = Output{Stdout: "cove abc123 (commit abc123, built now)\n"}
	r.outputs[key("cove", "shell", "ubuntu:ci", "--", "echo", "OK")] = Output{Stdout: "OK\n"}

	got := RunPrepare(context.Background(), PrepareConfig{CoveBin: "cove", Ref: "ubuntu:ci", Force: true, Runner: r})
	if got.Status != StatusPass {
		t.Fatalf("RunPrepare status = %q, want pass", got.Status)
	}
	if !contains(r.calls, key("cove", "shell", "ubuntu:ci", "--", "which", "docker")) {
		t.Fatalf("force did not run full checks: %v", r.calls)
	}
}

func TestRunPrepareRejectsRegistryRef(t *testing.T) {
	for _, ref := range []string{"localhost:5000/cove/audit:latest", "ghcr.io/tmc/cove/audit:latest"} {
		t.Run(ref, func(t *testing.T) {
			r := &fakeRunner{}
			got := RunPrepare(context.Background(), PrepareConfig{
				CoveBin: "cove",
				Ref:     ref,
				Force:   true,
				Runner:  r,
			})
			if got.Status != StatusFail {
				t.Fatalf("RunPrepare registry ref status = %q, want fail", got.Status)
			}
			if len(got.Checks) != 1 || got.Checks[0].Name != "image-ref" {
				t.Fatalf("checks = %#v, want single image-ref check", got.Checks)
			}
			if !strings.Contains(got.Checks[0].Message, "registry image refs are not supported") {
				t.Fatalf("message = %q, want registry unsupported hint", got.Checks[0].Message)
			}
			if len(r.calls) != 0 {
				t.Fatalf("RunPrepare registry ref made runner calls: %v", r.calls)
			}
		})
	}
}

func TestMovePrepareFlagsFirst(t *testing.T) {
	got := movePrepareFlagsFirst([]string{"runner:latest", "--json", "--force", "--ttl", "2h", "--cove-bin", "./cove", "--registry-ref", "ghcr.io/tmc/cove:latest"})
	want := []string{"--json", "--force", "--ttl", "2h", "--cove-bin", "./cove", "--registry-ref", "ghcr.io/tmc/cove:latest", "runner:latest"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("movePrepareFlagsFirst = %q, want %q", got, want)
	}
}

func TestRunPrepareCommandRegistryRef(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := RunPrepareCommand(context.Background(), []string{"--registry-ref", "ghcr.io/tmc/cove/test:latest", "--json"}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("RunPrepareCommand = 0, want failure; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "flag provided but not defined") {
		t.Fatalf("stderr = %q, want known --registry-ref flag", stderr.String())
	}
	if !strings.Contains(stdout.String(), "registry image refs are not supported") {
		t.Fatalf("stdout = %q, want registry unsupported report", stdout.String())
	}
}

func TestRunPrepareCommandHelpExitsZero(t *testing.T) {
	for _, arg := range []string{"-h", "help"} {
		var stdout, stderr bytes.Buffer
		if code := RunPrepareCommand(context.Background(), []string{arg}, &stdout, &stderr); code != 0 {
			t.Fatalf("RunPrepareCommand(%s) = %d, want 0; stderr=%q", arg, code, stderr.String())
		}
		if !strings.Contains(stderr.String(), "Usage: cove action prepare-image") {
			t.Fatalf("stderr = %q, want usage", stderr.String())
		}
		for _, want := range []string{"--registry-ref <ref>", "registry image refs are not supported"} {
			if !strings.Contains(stderr.String(), want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), want)
			}
		}
	}
}

func TestRunDoctorCommandHelpExitsZero(t *testing.T) {
	for _, arg := range []string{"-h", "help"} {
		var stdout, stderr bytes.Buffer
		if code := RunDoctorCommand(context.Background(), []string{arg}, &stdout, &stderr); code != 0 {
			t.Fatalf("RunDoctorCommand(%s) = %d, want 0; stderr=%q", arg, code, stderr.String())
		}
		if !strings.Contains(stderr.String(), "Usage: cove action doctor") {
			t.Fatalf("stderr = %q, want usage", stderr.String())
		}
	}
}

func TestParseVersionToken(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"cove", "cove abc123 (commit abc123, built now)", "abc123"},
		{"agent", "vz-agent def456\n", "def456"},
		{"missing", "version abc123\n", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseVersionToken(tt.in); got != tt.want {
				t.Fatalf("parseVersionToken(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return tm
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
