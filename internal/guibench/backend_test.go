package guibench

import (
	"strings"
	"testing"
)

func TestStepBudget(t *testing.T) {
	tests := []struct {
		name       string
		complexity int
		want       int
	}{
		{"zero falls to floor", 0, 15},
		{"negative falls to floor", -3, 15},
		{"one unit", 1, 30},
		{"three units", 3, 60},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StepBudget(tt.complexity); got != tt.want {
				t.Errorf("StepBudget(%d) = %d, want %d", tt.complexity, got, tt.want)
			}
		})
	}
}

// TestCanRun reuses the package's fakeBackend (runner_test.go), which answers
// MaxTier from its tier field, so CanRun is tested without a VM.
func TestCanRun(t *testing.T) {
	tierA := &Task{ID: "a", Evaluator: Evaluator{Func: StringList{"exact_match"}, Result: GetterSpec{Kind: "exec", Args: []string{"true"}}}}
	tierB := &Task{ID: "b", Evaluator: Evaluator{Func: StringList{"exact_match"}, Result: GetterSpec{Kind: "sqlite", Path: "/db", Query: "select 1"}}}
	tierC := &Task{ID: "c", Evaluator: Evaluator{Func: StringList{"exact_match"}, Result: GetterSpec{Kind: "accessibility", App: "Safari", Attr: "AXTitle"}}}

	tests := []struct {
		name    string
		backend Backend
		tasks   []*Task
		wantErr bool
	}{
		{"tierA backend runs tierA corpus", &fakeBackend{tier: TierA}, []*Task{tierA}, false},
		{"tierA backend rejects tierB corpus", &fakeBackend{tier: TierA}, []*Task{tierA, tierB}, true},
		{"tierB backend runs tierA+B corpus", &fakeBackend{tier: TierB}, []*Task{tierA, tierB}, false},
		{"tierB backend rejects tierC corpus", &fakeBackend{tier: TierB}, []*Task{tierC}, true},
		{"tierC backend runs everything", &fakeBackend{tier: TierC}, []*Task{tierA, tierB, tierC}, false},
		{"empty corpus runs on any backend", &fakeBackend{tier: TierA}, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CanRun(tt.backend, tt.tasks)
			if tt.wantErr && err == nil {
				t.Fatalf("CanRun: want error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("CanRun: unexpected error: %v", err)
			}
			if tt.wantErr && !strings.Contains(err.Error(), "corpus needs tier") {
				t.Errorf("CanRun error %q does not name the tier gap", err)
			}
		})
	}
}
