package vmpolicy

import (
	"testing"
	"time"
)

func TestPolicyBoundaryPersistence(t *testing.T) {
	tests := []struct {
		name string
		in   Policy
		want Policy
	}{
		{
			name: "idle timeout smallest positive",
			in:   Policy{IdleTimeout: time.Nanosecond},
			want: Policy{IdleTimeout: time.Nanosecond},
		},
		{
			name: "max age smallest positive",
			in:   Policy{MaxAge: time.Nanosecond},
			want: Policy{MaxAge: time.Nanosecond},
		},
		{
			name: "run budget one",
			in:   Policy{RunBudget: 1},
			want: Policy{RunBudget: 1},
		},
		{
			name: "zero values stay empty",
			in:   Policy{},
			want: Policy{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := Save(dir, tt.in); err != nil {
				t.Fatalf("Save: %v", err)
			}
			got, err := Load(dir)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got != tt.want {
				t.Fatalf("Load = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestPolicyMergeOverridePrecedence(t *testing.T) {
	base := Policy{IdleTimeout: time.Minute, MaxAge: time.Hour, RunBudget: 3}
	tests := []struct {
		name   string
		update Policy
		want   Policy
	}{
		{
			name:   "zero update preserves base",
			update: Policy{},
			want:   base,
		},
		{
			name:   "idle overrides idle only",
			update: Policy{IdleTimeout: 2 * time.Minute},
			want:   Policy{IdleTimeout: 2 * time.Minute, MaxAge: time.Hour, RunBudget: 3},
		},
		{
			name:   "max age overrides max age only",
			update: Policy{MaxAge: 2 * time.Hour},
			want:   Policy{IdleTimeout: time.Minute, MaxAge: 2 * time.Hour, RunBudget: 3},
		},
		{
			name:   "run budget overrides run budget only",
			update: Policy{RunBudget: 1},
			want:   Policy{IdleTimeout: time.Minute, MaxAge: time.Hour, RunBudget: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := base.Merge(tt.update); got != tt.want {
				t.Fatalf("Merge = %#v, want %#v", got, tt.want)
			}
		})
	}
}
