package lifecycle

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCounterPath(t *testing.T) {
	got := CounterPath("/tmp/vm")
	want := filepath.Join("/tmp/vm", "runs.counter")
	if got != want {
		t.Fatalf("CounterPath = %q, want %q", got, want)
	}
}

func TestRunsUsedMissingFile(t *testing.T) {
	used, err := RunsUsed(t.TempDir())
	if err != nil {
		t.Fatalf("RunsUsed: %v", err)
	}
	if used != 0 {
		t.Fatalf("RunsUsed = %d, want 0", used)
	}
}

func TestRunsUsedParse(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    int
		wantErr string
	}{
		{"empty", "", 0, ""},
		{"whitespace", "  \n", 0, ""},
		{"valid", "7\n", 7, ""},
		{"valid_no_newline", "3", 3, ""},
		{"malformed", "not-a-number", 0, "parse run counter"},
		{"negative", "-1\n", 0, "negative count"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(CounterPath(dir), []byte(tt.content), 0644); err != nil {
				t.Fatal(err)
			}
			got, err := RunsUsed(dir)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("RunsUsed err = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("RunsUsed: %v", err)
			}
			if got != tt.want {
				t.Fatalf("RunsUsed = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestConsumeRunBudgetSequential(t *testing.T) {
	dir := t.TempDir()
	for i := 1; i <= 3; i++ {
		got, err := ConsumeRunBudget(dir, 3)
		if err != nil {
			t.Fatalf("ConsumeRunBudget #%d: %v", i, err)
		}
		if got != i {
			t.Fatalf("ConsumeRunBudget #%d = %d, want %d", i, got, i)
		}
	}
	used, err := ConsumeRunBudget(dir, 3)
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("ConsumeRunBudget overflow err = %v, want ErrBudgetExceeded", err)
	}
	if used != 3 {
		t.Fatalf("ConsumeRunBudget overflow used = %d, want 3", used)
	}
}

func TestConsumeRunBudgetMalformed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(CounterPath(dir), []byte("garbage"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ConsumeRunBudget(dir, 5); err == nil {
		t.Fatal("ConsumeRunBudget on malformed counter: expected error")
	}
}

func TestConsumeRunBudgetCreatesVMDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "vm")
	if _, err := ConsumeRunBudget(dir, 1); err != nil {
		t.Fatalf("ConsumeRunBudget: %v", err)
	}
	if _, err := os.Stat(CounterPath(dir)); err != nil {
		t.Fatalf("counter file missing: %v", err)
	}
}
