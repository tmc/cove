package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/tmc/vz-macos/internal/runs"
)

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

func TestParseRunsExportArgsErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"missing format value", []string{"abc", "--format"}, "requires a value"},
		{"unknown flag", []string{"abc", "--bogus"}, "unknown runs export flag"},
		{"two prefixes", []string{"abc", "def", "--format=json"}, "usage:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := parseRunsExportArgs(tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v; want contains %q", err, tt.want)
			}
		})
	}
}

func TestRunRunsExportRejectsUnknownFormat(t *testing.T) {
	err := runRunsExport([]string{"abc123", "--format=yaml"})
	if err == nil || !strings.Contains(err.Error(), "unknown runs export format") {
		t.Fatalf("err = %v; want unknown format error", err)
	}
}

func TestRunRunsExportRequiresPrefixAndFormat(t *testing.T) {
	tests := [][]string{
		{},
		{"--format=json"},
		{"abc123"},
	}
	for _, args := range tests {
		err := runRunsExport(args)
		if err == nil || !strings.Contains(err.Error(), "usage:") {
			t.Fatalf("runRunsExport(%v) err = %v; want usage error", args, err)
		}
	}
}

func TestPrintRunsTableIncludesEventCount(t *testing.T) {
	exit := 0
	summaries := []runs.Summary{{
		RunID:           "20260510-abcdef",
		ImageRef:        "ubuntu:24.04",
		VMName:          "vm1",
		Status:          "ok",
		TotalDurationMS: 250,
		EventCount:      7,
		ExitCode:        &exit,
		StartedAt:       time.Unix(0, 0).UTC(),
	}}
	var buf bytes.Buffer
	if err := printRunsTable(&buf, summaries); err != nil {
		t.Fatalf("printRunsTable: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "EVENTS") {
		t.Fatalf("missing EVENTS header:\n%s", out)
	}
	if !strings.Contains(out, " 7 ") {
		t.Fatalf("missing event count cell '7':\n%s", out)
	}
}
