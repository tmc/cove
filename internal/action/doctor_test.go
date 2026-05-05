package action

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

type fakeDoctorRunner struct {
	out   map[string]Output
	err   map[string]error
	calls []string
}

func (r *fakeDoctorRunner) Run(_ context.Context, name string, args ...string) (Output, error) {
	key := name + "\x00" + strings.Join(args, "\x00")
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	return r.out[key], r.err[key]
}

func TestReportExitCode(t *testing.T) {
	tests := []struct {
		name string
		in   Report
		want int
	}{
		{name: "pass", in: Report{Status: StatusPass}, want: 0},
		{name: "warn", in: Report{Status: StatusWarn}, want: 2},
		{name: "fail", in: Report{Status: StatusFail}, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.in.ExitCode(); got != tt.want {
				t.Fatalf("ExitCode() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestRunDoctorPass(t *testing.T) {
	dir := t.TempDir()
	runner := goodDoctorRunner()
	report := RunDoctor(context.Background(), DoctorConfig{
		CoveBin:  "/tmp/cove",
		VZDir:    filepath.Join(dir, ".vz"),
		RunsDir:  filepath.Join(dir, ".vz", "runs"),
		Arch:     "arm64",
		CPUBrand: "Apple M4",
		Runner:   runner,
		Statfs: func(string) (DiskInfo, error) {
			return DiskInfo{FreeBytes: 64 * gib}, nil
		},
		AgentHook: func(context.Context, DoctorConfig) CheckResult {
			return CheckResult{Name: "agent-version", Status: StatusPass, Message: "agent hook passed"}
		},
		ForkFromHook: func(context.Context, DoctorConfig) CheckResult {
			return CheckResult{Name: "fork-from", Status: StatusPass, Message: "fork hook passed"}
		},
	})
	if report.Status != StatusPass || report.ExitCode() != 0 {
		t.Fatalf("RunDoctor = %q exit %d, want pass/0: %#v", report.Status, report.ExitCode(), report.Checks)
	}
	wantStatuses(t, report, map[string]Status{
		"apple-silicon":              StatusPass,
		"codesign":                   StatusPass,
		"virtualization-entitlement": StatusPass,
		"disk-capacity":              StatusPass,
		"runs-writable":              StatusPass,
		"network":                    StatusPass,
		"cove-version-floor":         StatusPass,
		"agent-version":              StatusPass,
		"fork-from":                  StatusPass,
	})
	if !contains(runner.calls, "/tmp/cove ctl network list") {
		t.Fatalf("calls missing cove ctl network list: %v", runner.calls)
	}
}

func TestRunDoctorDiskThresholds(t *testing.T) {
	tests := []struct {
		name string
		free uint64
		want Status
		code int
	}{
		{name: "fail under 20", free: 19 * gib, want: StatusFail, code: 1},
		{name: "warn under 30", free: 29 * gib, want: StatusWarn, code: 2},
		{name: "pass at 30", free: 30 * gib, want: StatusPass, code: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := goodDoctorConfig(t)
			cfg.Statfs = func(string) (DiskInfo, error) {
				return DiskInfo{FreeBytes: tt.free}, nil
			}
			report := RunDoctor(context.Background(), cfg)
			if got := statusOf(report, "disk-capacity"); got != tt.want {
				t.Fatalf("disk status = %q, want %q: %#v", got, tt.want, report.Checks)
			}
			if got := report.ExitCode(); got != tt.code {
				t.Fatalf("ExitCode() = %d, want %d", got, tt.code)
			}
		})
	}
}

func TestRunDoctorAppleSiliconCheck(t *testing.T) {
	tests := []struct {
		name  string
		arch  string
		brand string
		want  Status
	}{
		{name: "apple", arch: "arm64", brand: "Apple M4", want: StatusPass},
		{name: "intel", arch: "amd64", brand: "Intel", want: StatusFail},
		{name: "non apple arm", arch: "arm64", brand: "Ampere", want: StatusFail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := goodDoctorConfig(t)
			cfg.Arch = tt.arch
			cfg.CPUBrand = tt.brand
			report := RunDoctor(context.Background(), cfg)
			if got := statusOf(report, "apple-silicon"); got != tt.want {
				t.Fatalf("apple-silicon status = %q, want %q: %#v", got, tt.want, report.Checks)
			}
		})
	}
}

func TestRunDoctorVersionFloor(t *testing.T) {
	cfg := goodDoctorConfig(t)
	report := RunDoctor(context.Background(), cfg)
	if got := statusOf(report, "cove-version-floor"); got != StatusPass {
		t.Fatalf("version floor status = %q, want pass: %#v", got, report.Checks)
	}

	cfg = goodDoctorConfig(t)
	runner := cfg.Runner.(*fakeDoctorRunner)
	runner.out["/tmp/cove\x00version"] = Output{Stdout: "cove old\n"}
	report = RunDoctor(context.Background(), cfg)
	if got := statusOf(report, "cove-version-floor"); got != StatusWarn {
		t.Fatalf("version floor status = %q, want warn: %#v", got, report.Checks)
	}
}

func TestRunDoctorVsockLatency(t *testing.T) {
	cfg := goodDoctorConfig(t)
	cfg.VMName = "runner"
	report := RunDoctor(context.Background(), cfg)
	if got := statusOf(report, "vsock-latency"); got != StatusPass {
		t.Fatalf("vsock-latency status = %q, want pass: %#v", got, report.Checks)
	}

	cfg = goodDoctorConfig(t)
	cfg.VMName = "runner"
	runner := cfg.Runner.(*fakeDoctorRunner)
	runner.err["/tmp/cove\x00ctl\x00-vm\x00runner\x00agent-ping"] = errors.New("exit status 1")
	runner.out["/tmp/cove\x00ctl\x00-vm\x00runner\x00agent-ping"] = Output{Stderr: "unavailable"}
	report = RunDoctor(context.Background(), cfg)
	if got := statusOf(report, "vsock-latency"); got != StatusFail {
		t.Fatalf("vsock-latency status = %q, want fail: %#v", got, report.Checks)
	}
}

func TestRunDoctorFailures(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*DoctorConfig, *fakeDoctorRunner)
		wantName string
	}{
		{
			name: "unsigned",
			mutate: func(_ *DoctorConfig, r *fakeDoctorRunner) {
				r.err["codesign\x00-dv\x00/tmp/cove"] = errors.New("exit status 1")
				r.out["codesign\x00-dv\x00/tmp/cove"] = Output{Stderr: "code object is not signed"}
			},
			wantName: "codesign",
		},
		{
			name: "missing entitlement",
			mutate: func(_ *DoctorConfig, r *fakeDoctorRunner) {
				r.out["codesign\x00-d\x00--entitlements\x00-\x00/tmp/cove"] = Output{Stdout: "<plist/>"}
			},
			wantName: "virtualization-entitlement",
		},
		{
			name: "network command fails",
			mutate: func(_ *DoctorConfig, r *fakeDoctorRunner) {
				r.err["/tmp/cove\x00ctl\x00network\x00list"] = errors.New("exit status 1")
				r.out["/tmp/cove\x00ctl\x00network\x00list"] = Output{Stderr: "unknown ctl command"}
				r.err["/tmp/cove\x00network\x00list"] = errors.New("exit status 1")
				r.out["/tmp/cove\x00network\x00list"] = Output{Stderr: "network unavailable"}
			},
			wantName: "network",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := goodDoctorConfig(t)
			runner := cfg.Runner.(*fakeDoctorRunner)
			tt.mutate(&cfg, runner)
			report := RunDoctor(context.Background(), cfg)
			if got := statusOf(report, tt.wantName); got != StatusFail {
				t.Fatalf("%s status = %q, want fail: %#v", tt.wantName, got, report.Checks)
			}
			if got := report.ExitCode(); got != 1 {
				t.Fatalf("ExitCode() = %d, want 1", got)
			}
		})
	}
}

func TestForkFromEnvHook(t *testing.T) {
	t.Setenv("COVE_ACTION_FORK_FROM", "ubuntu-ci")
	cfg := goodDoctorConfig(t)
	report := RunDoctor(context.Background(), cfg)
	if got := statusOf(report, "fork-from-cache"); got != StatusPass {
		t.Fatalf("fork-from-cache status = %q, want pass: %#v", got, report.Checks)
	}
}

func TestForkFromCacheColdWarns(t *testing.T) {
	cfg := goodDoctorConfig(t)
	cfg.ForkFrom = "broken"
	runner := cfg.Runner.(*fakeDoctorRunner)
	runner.err["/tmp/cove\x00image\x00inspect\x00-json\x00broken"] = errors.New("exit status 1")
	report := RunDoctor(context.Background(), cfg)
	if got := statusOf(report, "fork-from-cache"); got != StatusWarn {
		t.Fatalf("fork-from-cache status = %q, want warn: %#v", got, report.Checks)
	}
}

func TestDoctorMainJSON(t *testing.T) {
	opts, err := ParseDoctorArgs([]string{"--json", "--cove-bin", "/tmp/cove", "--vz-dir", "/tmp/vz", "--vm", "runner"})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.JSON || opts.Config.CoveBin != "/tmp/cove" || opts.Config.VZDir != "/tmp/vz" || opts.Config.VMName != "runner" {
		t.Fatalf("ParseDoctorArgs = %#v", opts)
	}
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

func goodDoctorConfig(t *testing.T) DoctorConfig {
	t.Helper()
	dir := t.TempDir()
	return DoctorConfig{
		CoveBin:  "/tmp/cove",
		VZDir:    filepath.Join(dir, ".vz"),
		RunsDir:  filepath.Join(dir, ".vz", "runs"),
		Arch:     "arm64",
		CPUBrand: "Apple M4",
		Runner:   goodDoctorRunner(),
		Statfs: func(string) (DiskInfo, error) {
			return DiskInfo{FreeBytes: 64 * gib}, nil
		},
	}
}

func goodDoctorRunner() *fakeDoctorRunner {
	return &fakeDoctorRunner{
		out: map[string]Output{
			"codesign\x00-dv\x00/tmp/cove":                                {Stderr: "Executable=/tmp/cove\nSignature=adhoc\n"},
			"codesign\x00-d\x00--entitlements\x00-\x00/tmp/cove":          {Stdout: "<key>com.apple.security.virtualization</key><true/>"},
			"sysctl\x00-n\x00machdep.cpu.brand_string":                    {Stdout: "Apple M4\n"},
			"/tmp/cove\x00version":                                        {Stdout: "cove f1e6812 design030 slice2-cache\n"},
			"/tmp/cove\x00ctl\x00network\x00list":                         {Stdout: "nat\nbridged:en0\n"},
			"/tmp/cove\x00ctl\x00-vm\x00runner\x00agent-ping":             {Stdout: "pong\n"},
			"/tmp/cove\x00image\x00inspect\x00-json\x00ubuntu-ci":         {Stdout: `{"ref":"ubuntu-ci","features":["agent"]}`},
			"/tmp/cove\x00image\x00inspect\x00-json\x00missing-agent":     {Stdout: `{"ref":"missing-agent"}`},
			"/tmp/cove\x00image\x00inspect\x00-json\x00broken":            {Stderr: "not found"},
			"/tmp/cove\x00image\x00inspect\x00-json\x00agent-in-stderr":   {Stderr: "agent"},
			"/tmp/cove\x00image\x00inspect\x00-json\x00agent-in-stdout":   {Stdout: "agent"},
			"/tmp/cove\x00image\x00inspect\x00-json\x00agent-in-metadata": {Stdout: `"agent_version":"v0"`},
		},
		err: map[string]error{},
	}
}

func wantStatuses(t *testing.T, report Report, want map[string]Status) {
	t.Helper()
	for name, status := range want {
		if got := statusOf(report, name); got != status {
			t.Fatalf("%s status = %q, want %q; %#v", name, got, status, report.Checks)
		}
	}
}

func statusOf(report Report, name string) Status {
	for _, check := range report.Checks {
		if check.Name == name {
			return check.Status
		}
	}
	return ""
}
