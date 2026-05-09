package action

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestParseDoctorArgsErrors covers ParseDoctorArgs failure branches.
func TestParseDoctorArgsErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "unexpected positional", args: []string{"--json", "extra"}},
		{name: "unknown flag", args: []string{"--no-such-flag"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseDoctorArgs(tt.args); err == nil {
				t.Fatalf("ParseDoctorArgs(%v) err = nil, want error", tt.args)
			}
		})
	}
}

// TestDoctorMainExitCodes verifies DoctorMain wires WriteReport + ExitCode.
func TestDoctorMainExitCodes(t *testing.T) {
	t.Run("parse error", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := DoctorMain(context.Background(), []string{"--no-such-flag"}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("DoctorMain code = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "cove action doctor:") {
			t.Fatalf("stderr = %q, want diagnostic prefix", stderr.String())
		}
	})
}

// TestWriteReportJSONShape pins the JSON serialization shape for Report.
func TestWriteReportJSONShape(t *testing.T) {
	report := Report{
		OK:     false,
		Status: StatusFail,
		Checks: []CheckResult{
			{Name: "apple-silicon", Status: StatusFail, Message: "host arch amd64"},
			{Name: "disk-capacity", Status: StatusPass},
		},
	}
	var buf bytes.Buffer
	if err := WriteReport(&buf, report, true); err != nil {
		t.Fatalf("WriteReport: %v", err)
	}
	var got Report
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, buf.String())
	}
	if got.OK || got.Status != StatusFail || len(got.Checks) != 2 {
		t.Fatalf("decoded = %#v", got)
	}
	if got.Checks[0].Name != "apple-silicon" || got.Checks[0].Message != "host arch amd64" {
		t.Fatalf("check[0] = %#v", got.Checks[0])
	}
	// Ensure empty Message is omitted for the pass entry.
	if strings.Contains(buf.String(), `"message": ""`) {
		t.Fatalf("empty message not omitted: %s", buf.String())
	}
}

// TestForkFromCacheWarmNoAgent covers the warm-but-missing-agent warn branch.
func TestForkFromCacheWarmNoAgent(t *testing.T) {
	cfg := goodDoctorConfig(t)
	cfg.ForkFrom = "no-feature-meta"
	r := cfg.Runner.(*fakeDoctorRunner)
	r.out["/tmp/cove\x00image\x00inspect\x00-json\x00no-feature-meta"] = Output{Stdout: `{"ref":"no-feature-meta"}`}
	report := RunDoctor(context.Background(), cfg)
	got := statusOf(report, "fork-from-cache")
	if got != StatusWarn {
		t.Fatalf("fork-from-cache = %q, want warn: %#v", got, report.Checks)
	}
}

// TestAppleSiliconBrandUnavailableWarns covers the warn-when-brand-empty branch.
func TestAppleSiliconBrandUnavailableWarns(t *testing.T) {
	cfg := goodDoctorConfig(t)
	cfg.CPUBrand = ""
	r := cfg.Runner.(*fakeDoctorRunner)
	r.out["sysctl\x00-n\x00machdep.cpu.brand_string"] = Output{}
	r.err["sysctl\x00-n\x00machdep.cpu.brand_string"] = errSysctlFail{}
	report := RunDoctor(context.Background(), cfg)
	if got := statusOf(report, "apple-silicon"); got != StatusWarn {
		t.Fatalf("apple-silicon = %q, want warn: %#v", got, report.Checks)
	}
}

type errSysctlFail struct{}

func (errSysctlFail) Error() string { return "sysctl unavailable" }

// TestMakeReportAggregation pins fail > warn > pass precedence.
func TestMakeReportAggregation(t *testing.T) {
	tests := []struct {
		name   string
		checks []CheckResult
		want   Status
		ok     bool
	}{
		{name: "all pass", checks: []CheckResult{{Status: StatusPass}, {Status: StatusPass}}, want: StatusPass, ok: true},
		{name: "warn dominates pass", checks: []CheckResult{{Status: StatusPass}, {Status: StatusWarn}}, want: StatusWarn},
		{name: "fail dominates warn", checks: []CheckResult{{Status: StatusWarn}, {Status: StatusFail}, {Status: StatusPass}}, want: StatusFail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := makeReport(tt.checks)
			if got.Status != tt.want || got.OK != tt.ok {
				t.Fatalf("makeReport = %+v, want status=%q ok=%v", got, tt.want, tt.ok)
			}
		})
	}
}
