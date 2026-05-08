package storagecensus

import (
	"bytes"
	"math"
	"testing"
)

// TestThresholdHelpersBoundary covers the budget threshold math at the
// edges flagged by R68 inventory: zero target, MaxInt64 target with
// pct=100, and pct=100 on a small target (should equal target).
//
// These are public-surface boundary tests; if any failure here looks
// like a real bug rather than spec ambiguity, flag it — do not fix.
func TestThresholdHelpersBoundary(t *testing.T) {
	const maxI = int64(math.MaxInt64)
	tests := []struct {
		name     string
		target   int64
		warnPct  int
		hardPct  int
		wantWarn int64
		wantHard int64
	}{
		// IsSet is false when target is zero, so thresholds collapse to 0
		// regardless of pct. This is the "no budget => no tripwire" rule.
		{"zero-target-with-pct", 0, 80, 95, 0, 0},
		// pct=100 on a normal target should equal the target itself.
		{"pct-100-small", 1000, 100, 100, 1000, 1000},
		// pct=100 on max int64. Validate that target/100*100 does not
		// exceed target (would indicate overflow). The /100*100 rounding
		// rule means the result is target rounded down to a multiple
		// of 100 — document, do not "fix".
		{"max-target-pct-100", maxI, 100, 100, maxI / 100 * 100, maxI / 100 * 100},
		// pct=1 on max int64 — confirms divide-before-multiply order.
		{"max-target-pct-1", maxI, 1, 1, maxI / 100, maxI / 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := Budget{TargetBytes: tt.target, WarnPct: tt.warnPct, HardPct: tt.hardPct}
			if got := b.WarnBytes(); got != tt.wantWarn {
				t.Errorf("WarnBytes() = %d, want %d", got, tt.wantWarn)
			}
			if got := b.HardBytes(); got != tt.wantHard {
				t.Errorf("HardBytes() = %d, want %d", got, tt.wantHard)
			}
			if tt.target > 0 {
				if b.WarnBytes() > tt.target {
					t.Errorf("WarnBytes %d exceeds target %d (overflow?)", b.WarnBytes(), tt.target)
				}
				if b.HardBytes() > tt.target {
					t.Errorf("HardBytes %d exceeds target %d (overflow?)", b.HardBytes(), tt.target)
				}
			}
		})
	}
}

// TestValidateBoundary covers exact-edge percentages and the
// target=MaxInt64 case (Validate has no upper bound on target_bytes,
// so a max-value target is valid input).
func TestValidateBoundary(t *testing.T) {
	tests := []struct {
		name    string
		in      Budget
		wantErr bool
	}{
		{"warn-exactly-0", Budget{TargetBytes: 1, WarnPct: 0, HardPct: 0}, false},
		{"warn-exactly-100", Budget{TargetBytes: 1, WarnPct: 100, HardPct: 100}, false},
		{"hard-exactly-100-warn-0", Budget{TargetBytes: 1, HardPct: 100}, false},
		{"target-max-int64", Budget{TargetBytes: math.MaxInt64, WarnPct: 80, HardPct: 95}, false},
		{"target-zero-with-invalid-pct", Budget{TargetBytes: 0, WarnPct: 200}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.in.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() err=%v, wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

// TestReportStateBoundary covers exact-threshold transitions: usage
// equal to WarnBytes and HardBytes. The implementation is "used >=
// threshold" so both edges round up to the higher state.
func TestReportStateBoundary(t *testing.T) {
	b := Budget{TargetBytes: 1000, WarnPct: 80, HardPct: 95}
	tests := []struct {
		name string
		used int64
		want State
	}{
		{"one-below-warn", 799, StateOK},
		{"exactly-warn", 800, StateWarn},
		{"one-below-hard", 949, StateWarn},
		{"exactly-hard", 950, StateHard},
		{"used-zero", 0, StateOK},
		// Negative used has no spec; record current behavior so a
		// future change is intentional.
		{"used-negative", -1, StateOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep := Report{UsedBytes: tt.used, Budget: &b}
			if got := rep.State(); got != tt.want {
				t.Errorf("State() = %s, want %s", got, tt.want)
			}
		})
	}
}

// TestReportStateOnlyHardSet exercises the case where the operator
// configures a hard tripwire without a warn tripwire. WarnPct=0
// disables the warn band, so usage skips StateWarn entirely.
func TestReportStateOnlyHardSet(t *testing.T) {
	b := Budget{TargetBytes: 1000, HardPct: 90}
	tests := []struct {
		used int64
		want State
	}{
		{500, StateOK},
		{899, StateOK},
		{900, StateHard},
	}
	for _, tt := range tests {
		rep := Report{UsedBytes: tt.used, Budget: &b}
		if got := rep.State(); got != tt.want {
			t.Errorf("used=%d State=%s, want %s", tt.used, got, tt.want)
		}
	}
}

// TestRenderHumanHeadroomNegative confirms that when usage exceeds the
// target, RenderHuman still completes and the [HARD] marker is shown.
// The headroom value will be negative; we just assert no crash and the
// marker is present.
func TestRenderHumanHeadroomNegative(t *testing.T) {
	b := Budget{TargetBytes: 1000, WarnPct: 80, HardPct: 95}
	rep := Report{Root: "/x", UsedBytes: 5000, Budget: &b}
	var buf bytes.Buffer
	if err := RenderHuman(&buf, rep); err != nil {
		t.Fatalf("RenderHuman: %v", err)
	}
	out := buf.String()
	if !contains(out, "[HARD]") {
		t.Errorf("missing [HARD] marker on over-budget report:\n%s", out)
	}
	if !contains(out, "Headroom:") {
		t.Errorf("missing Headroom row:\n%s", out)
	}
}
