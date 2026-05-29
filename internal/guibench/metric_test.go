package guibench

import (
	"math"
	"testing"
)

func TestMetrics(t *testing.T) {
	tests := []struct {
		name     string
		metric   string
		result   string
		expected string
		options  map[string]any
		want     float64
		wantErr  bool
	}{
		// exact_match
		{"exact match", "exact_match", "Project", "Project", nil, 1, false},
		{"exact non-match", "exact_match", "project", "Project", nil, 0, false},
		{"exact empty equal", "exact_match", "", "", nil, 1, false},
		{"exact unicode", "exact_match", "café", "café", nil, 1, false},

		// must_include
		{"include hit", "must_include", "hello world", "world", nil, 1, false},
		{"include miss", "must_include", "hello world", "globe", nil, 0, false},
		{"include empty needle", "must_include", "anything", "", nil, 1, false},

		// fuzzy_match
		{"fuzzy whitespace", "fuzzy_match", "  Hello   World ", "hello world", nil, 1, false},
		{"fuzzy case", "fuzzy_match", "HELLO", "hello", nil, 1, false},
		{"fuzzy miss", "fuzzy_match", "hello there", "hello world", nil, 0, false},
		{"fuzzy unicode", "fuzzy_match", " CAFÉ ", "café", nil, 1, false},

		// file_exists
		{"file true", "file_exists", "true", "", nil, 1, false},
		{"file one", "file_exists", "1", "", nil, 1, false},
		{"file false", "file_exists", "false", "", nil, 0, false},
		{"file empty", "file_exists", "", "", nil, 0, false},

		// hash_equals
		{"hash equal", "hash_equals", "ABC123", "abc123", nil, 1, false},
		{"hash trimmed", "hash_equals", " abc \n", "abc", nil, 1, false},
		{"hash miss", "hash_equals", "abc", "def", nil, 0, false},
		{"hash empty expected", "hash_equals", "abc", "", nil, 0, true},

		// plist_equals
		{"plist equal", "plist_equals", "Dark\n", "Dark", nil, 1, false},
		{"plist miss", "plist_equals", "Light", "Dark", nil, 0, false},

		// sqlite_row_matches
		{"sqlite equal", "sqlite_row_matches", "Alice", "Alice", nil, 1, false},
		{"sqlite miss", "sqlite_row_matches", "alice", "Alice", nil, 0, false},
		{"sqlite ignore case", "sqlite_row_matches", "alice", "Alice", map[string]any{"ignore_case": true}, 1, false},
		{"sqlite bad option", "sqlite_row_matches", "a", "a", map[string]any{"ignore_case": "yes"}, 0, true},

		// url_in
		{"url in", "url_in", "https://example.com/page", "https://example.com", nil, 1, false},
		{"url trailing slash", "url_in", "https://example.com", "https://example.com/", nil, 1, false},
		{"url miss", "url_in", "https://other.com", "https://example.com", nil, 0, false},
		{"url empty expected", "url_in", "x", "", nil, 0, true},

		// infeasible
		{"infeasible fail", "infeasible", "FAIL", "", nil, 1, false},
		{"infeasible fail lower", "infeasible", " fail ", "", nil, 1, false},
		{"infeasible not fail", "infeasible", "done", "", nil, 0, false},
	}
	registry := Metrics()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, ok := registry[tt.metric]
			if !ok {
				t.Fatalf("metric %q not registered", tt.metric)
			}
			got, err := m(tt.result, tt.expected, tt.options)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got score %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if math.Abs(got-tt.want) > 1e-9 {
				t.Fatalf("score = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestScoreMetricsConjunction(t *testing.T) {
	tests := []struct {
		name     string
		funcs    StringList
		conj     string
		result   string
		expected string
		want     float64
		wantErr  bool
	}{
		{"and both pass", StringList{"must_include", "exact_match"}, "and", "abc", "abc", 1, false},
		{"and one fails", StringList{"must_include", "exact_match"}, "and", "abcd", "abc", 0.5, false},
		{"or one passes", StringList{"must_include", "exact_match"}, "or", "abcd", "abc", 1, false},
		{"or both fail", StringList{"exact_match", "exact_match"}, "or", "x", "y", 0, false},
		{"single no conj", StringList{"exact_match"}, "", "a", "a", 1, false},
		{"list bad conj", StringList{"exact_match", "must_include"}, "xor", "a", "a", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ScoreMetrics(Evaluator{Func: tt.funcs, Conj: tt.conj}, tt.result, tt.expected, nil)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if math.Abs(got-tt.want) > 1e-9 {
				t.Fatalf("score = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestScoreMetricsExpectedOption(t *testing.T) {
	// A literal expected value computed from params overrides the getter value.
	e := Evaluator{
		Func:    StringList{"exact_match"},
		Options: map[string]any{"expected": "Note-{N}"},
	}
	got, err := ScoreMetrics(e, "Note-42", "", map[string]string{"N": "42"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 1 {
		t.Fatalf("score = %v, want 1 (expected option materialized)", got)
	}
}
