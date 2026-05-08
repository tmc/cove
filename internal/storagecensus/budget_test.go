package storagecensus

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadBudgetMissingReturnsZero(t *testing.T) {
	root := t.TempDir()
	b, err := LoadBudget(root)
	if err != nil {
		t.Fatalf("LoadBudget on empty dir: %v", err)
	}
	if b.IsSet() {
		t.Errorf("zero-value budget reports IsSet")
	}
	if got := b.TargetBytes; got != 0 {
		t.Errorf("TargetBytes = %d, want 0", got)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	root := t.TempDir()
	want := Budget{TargetBytes: 500 * 1024 * 1024 * 1024, WarnPct: 80, HardPct: 95}
	if err := SaveBudget(root, want); err != nil {
		t.Fatalf("SaveBudget: %v", err)
	}
	got, err := LoadBudget(root)
	if err != nil {
		t.Fatalf("LoadBudget: %v", err)
	}
	if got != want {
		t.Errorf("round-trip = %+v, want %+v", got, want)
	}
	// File should exist at the expected path.
	if _, err := os.Stat(filepath.Join(root, BudgetFilename)); err != nil {
		t.Errorf("storage-budget.json missing: %v", err)
	}
}

func TestClearBudgetRemovesFile(t *testing.T) {
	root := t.TempDir()
	if err := SaveBudget(root, Budget{TargetBytes: 1024}); err != nil {
		t.Fatalf("SaveBudget: %v", err)
	}
	if err := ClearBudget(root); err != nil {
		t.Fatalf("ClearBudget: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, BudgetFilename)); !os.IsNotExist(err) {
		t.Errorf("file still present after Clear: %v", err)
	}
	// Clear is idempotent.
	if err := ClearBudget(root); err != nil {
		t.Errorf("Clear on missing file: %v", err)
	}
}

func TestSaveRejectsInvalid(t *testing.T) {
	root := t.TempDir()
	cases := []Budget{
		{TargetBytes: -1},
		{TargetBytes: 1024, WarnPct: 150},
		{TargetBytes: 1024, HardPct: -5},
		{TargetBytes: 1024, WarnPct: 90, HardPct: 80}, // warn > hard
	}
	for _, b := range cases {
		if err := SaveBudget(root, b); err == nil {
			t.Errorf("SaveBudget(%+v) succeeded; want error", b)
		}
	}
	// File must not have been written.
	if _, err := os.Stat(filepath.Join(root, BudgetFilename)); !os.IsNotExist(err) {
		t.Errorf("invalid input wrote file: %v", err)
	}
}

func TestThresholdHelpers(t *testing.T) {
	b := Budget{TargetBytes: 1000, WarnPct: 80, HardPct: 95}
	if got := b.WarnBytes(); got != 800 {
		t.Errorf("WarnBytes = %d, want 800", got)
	}
	if got := b.HardBytes(); got != 950 {
		t.Errorf("HardBytes = %d, want 950", got)
	}
	zero := Budget{}
	if got := zero.WarnBytes(); got != 0 {
		t.Errorf("zero WarnBytes = %d, want 0", got)
	}
	if got := zero.HardBytes(); got != 0 {
		t.Errorf("zero HardBytes = %d, want 0", got)
	}
}

func TestReportStateAgainstBudget(t *testing.T) {
	b := Budget{TargetBytes: 1000, WarnPct: 80, HardPct: 95}
	cases := []struct {
		used int64
		want State
	}{
		{0, StateOK},
		{500, StateOK},
		{800, StateWarn},
		{900, StateWarn},
		{950, StateHard},
		{2000, StateHard},
	}
	for _, c := range cases {
		rep := Report{UsedBytes: c.used, Budget: &b}
		if got := rep.State(); got != c.want {
			t.Errorf("used=%d State=%s, want %s", c.used, got, c.want)
		}
	}
	// Without a budget, state is unset.
	rep := Report{UsedBytes: 1_000_000_000}
	if got := rep.State(); got != StateUnset {
		t.Errorf("nil-budget State=%s, want unset", got)
	}
	// Zero-value budget on a non-nil pointer also yields unset.
	zero := Budget{}
	rep2 := Report{UsedBytes: 1_000_000_000, Budget: &zero}
	if got := rep2.State(); got != StateUnset {
		t.Errorf("zero-budget State=%s, want unset", got)
	}
}

func TestRenderHumanWithBudgetShowsHeadroomAndMarker(t *testing.T) {
	b := Budget{TargetBytes: 1000, WarnPct: 80, HardPct: 95}
	rep := Report{Root: "/x", UsedBytes: 850, Budget: &b}
	var buf bytes.Buffer
	if err := RenderHuman(&buf, rep); err != nil {
		t.Fatalf("RenderHuman: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"Target:", "Headroom:", "[WARN]"} {
		if !contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestBudgetValidate(t *testing.T) {
	tests := []struct {
		name    string
		in      Budget
		wantErr bool
	}{
		{"zero", Budget{}, false},
		{"valid", Budget{TargetBytes: 1000, WarnPct: 80, HardPct: 95}, false},
		{"negative target", Budget{TargetBytes: -1}, true},
		{"warn below 0", Budget{TargetBytes: 1, WarnPct: -1}, true},
		{"warn above 100", Budget{TargetBytes: 1, WarnPct: 101}, true},
		{"hard below 0", Budget{TargetBytes: 1, HardPct: -1}, true},
		{"hard above 100", Budget{TargetBytes: 1, HardPct: 101}, true},
		{"warn exceeds hard", Budget{TargetBytes: 1, WarnPct: 95, HardPct: 80}, true},
		{"warn equals hard", Budget{TargetBytes: 1, WarnPct: 90, HardPct: 90}, false},
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

func TestEncodeBudgetJSON(t *testing.T) {
	tests := []struct {
		name string
		in   Budget
		want []string
	}{
		{"zero", Budget{}, []string{`"target_bytes": 0`}},
		{"set", Budget{TargetBytes: 1000, WarnPct: 80, HardPct: 95},
			[]string{`"target_bytes": 1000`, `"warn_pct": 80`, `"hard_pct": 95`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := EncodeBudgetJSON(&buf, tt.in); err != nil {
				t.Fatalf("EncodeBudgetJSON: %v", err)
			}
			out := buf.String()
			for _, w := range tt.want {
				if !contains(out, w) {
					t.Errorf("missing %q in output:\n%s", w, out)
				}
			}
		})
	}
}

func TestLoadBudgetCorruptFileReturnsError(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, BudgetFilename), []byte("not json"), 0o644); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}
	if _, err := LoadBudget(root); err == nil {
		t.Errorf("LoadBudget on corrupt file succeeded; want error")
	}
}
