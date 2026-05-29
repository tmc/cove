package main

import "testing"

func TestMissingValue(t *testing.T) {
	if got := missingValue(false, "x"); got != "<missing>" {
		t.Errorf("missingValue(false, _) = %v, want <missing>", got)
	}
	if got := missingValue(true, "x"); got != "x" {
		t.Errorf("missingValue(true, x) = %v, want x", got)
	}
	if got := missingValue(true, nil); got != nil {
		t.Errorf("missingValue(true, nil) = %v, want nil", got)
	}
}

func TestJSONValueEqual(t *testing.T) {
	cases := []struct {
		name string
		a, b any
		want bool
	}{
		{"equal strings", "abc", "abc", true},
		{"diff strings", "abc", "abd", false},
		{"equal numbers", 42, 42, true},
		{"diff numbers", 42, 43, false},
		{"nil vs nil", nil, nil, true},
		{"nil vs string", nil, "", false},
		{"equal maps", map[string]any{"k": "v"}, map[string]any{"k": "v"}, true},
		{"diff maps", map[string]any{"k": "v"}, map[string]any{"k": "w"}, false},
		{"equal slices", []any{"a", "b"}, []any{"a", "b"}, true},
		{"diff slices", []any{"a", "b"}, []any{"b", "a"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := jsonValueEqual(tc.a, tc.b); got != tc.want {
				t.Errorf("jsonValueEqual(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestInspectDiffString(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"string", "hello", "hello"},
		{"empty string", "", ""},
		{"any-slice", []any{"a", "b"}, "[a, b]"},
		{"empty any-slice", []any{}, "[]"},
		{"layer empty", imageInspectLayerValue{}, ""},
		{"layer populated", imageInspectLayerValue{Digest: "sha256:abc", Size: 12}, "sha256:abc (12 bytes)"},
		{"int", 7, "7"},
		{"bool", true, "true"},
		{"nil", nil, "null"},
		{"map", map[string]any{"k": "v"}, `{"k":"v"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := inspectDiffString(tc.in); got != tc.want {
				t.Errorf("inspectDiffString(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestInspectDiffListString(t *testing.T) {
	cases := []struct {
		name string
		in   []any
		want string
	}{
		{"empty", nil, "[]"},
		{"strings", []any{"a", "b", "c"}, "[a, b, c]"},
		{"single string", []any{"only"}, "[only]"},
		{"non-strings json-marshaled", []any{1, true, nil}, "[1, true, null]"},
		{"mixed", []any{"raw", 42, map[string]any{"k": "v"}}, `[raw, 42, {"k":"v"}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := inspectDiffListString(tc.in); got != tc.want {
				t.Errorf("inspectDiffListString(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
