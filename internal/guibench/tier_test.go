package guibench

import "testing"

func TestGetterTier(t *testing.T) {
	tests := []struct {
		kind string
		want Tier
	}{
		{"exec", TierA},
		{"file", TierA},
		{"defaults", TierA},
		{"screen_ocr", TierA},
		{"sqlite", TierB},
		{"protected_file", TierB},
		{"tccdb", TierB},
		{"applescript", TierC},
		{"accessibility", TierC},
	}
	for _, tt := range tests {
		if got := (GetterSpec{Kind: tt.kind}).Tier(); got != tt.want {
			t.Fatalf("kind %q tier = %q, want %q", tt.kind, got, tt.want)
		}
	}
}

func TestTierGrant(t *testing.T) {
	tests := []struct {
		tier Tier
		want string
	}{
		{TierA, "none"},
		{TierB, "Full Disk Access"},
		{TierC, "Apple Events + Accessibility"},
	}
	for _, tt := range tests {
		if got := tt.tier.Grant(); got != tt.want {
			t.Fatalf("%q.Grant() = %q, want %q", tt.tier, got, tt.want)
		}
	}
}

func TestMaxTier(t *testing.T) {
	mk := func(kind string) *Task {
		return &Task{
			ID:        "t-" + kind,
			Evaluator: Evaluator{Func: StringList{"exact_match"}, Result: GetterSpec{Kind: kind}},
		}
	}
	tests := []struct {
		name  string
		tasks []*Task
		want  Tier
	}{
		{"empty corpus is A", nil, TierA},
		{"all tier A", []*Task{mk("exec"), mk("file")}, TierA},
		{"one tier B lifts to B", []*Task{mk("exec"), mk("sqlite")}, TierB},
		{"one tier C lifts to C", []*Task{mk("exec"), mk("sqlite"), mk("applescript")}, TierC},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MaxTier(tt.tasks); got != tt.want {
				t.Fatalf("MaxTier = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestMaxTierExpectedGetter confirms an expected getter at a higher tier than
// the result getter still lifts the corpus tier.
func TestMaxTierExpectedGetter(t *testing.T) {
	expected := GetterSpec{Kind: "applescript", Script: "x"}
	task := &Task{
		ID: "mixed",
		Evaluator: Evaluator{
			Func:     StringList{"exact_match"},
			Result:   GetterSpec{Kind: "exec", Args: []string{"x"}},
			Expected: &expected,
		},
	}
	if got := MaxTier([]*Task{task}); got != TierC {
		t.Fatalf("MaxTier = %q, want C (expected getter is tier C)", got)
	}
}
