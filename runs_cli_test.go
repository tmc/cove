package main

import "testing"

func TestParseRunsShowArgsAllowsFlagsAfterPrefix(t *testing.T) {
	prefix, jsonOut, err := parseRunsShowArgs([]string{"abc123", "--json"})
	if err != nil {
		t.Fatalf("parseRunsShowArgs: %v", err)
	}
	if prefix != "abc123" || !jsonOut {
		t.Fatalf("prefix/json = %q/%v, want abc123/true", prefix, jsonOut)
	}
}

func TestParseRunsExportArgsAllowsFormatAfterPrefix(t *testing.T) {
	tests := [][]string{
		{"abc123", "--format", "gha-summary"},
		{"abc123", "--format=gha-summary"},
		{"--format", "gha-summary", "abc123"},
	}
	for _, args := range tests {
		prefix, format, err := parseRunsExportArgs(args)
		if err != nil {
			t.Fatalf("parseRunsExportArgs(%v): %v", args, err)
		}
		if prefix != "abc123" || format != "gha-summary" {
			t.Fatalf("parseRunsExportArgs(%v) = %q, %q; want abc123, gha-summary", args, prefix, format)
		}
	}
}
