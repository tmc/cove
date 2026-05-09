package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestUsageExitCode(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want int
	}{
		{"empty", nil, 2},
		{"help word", []string{"help"}, 0},
		{"short flag", []string{"-h"}, 0},
		{"long flag", []string{"--help"}, 0},
		{"other arg", []string{"run"}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := usageExitCode(tt.args); got != tt.want {
				t.Fatalf("usageExitCode(%v) = %d, want %d", tt.args, got, tt.want)
			}
		})
	}
}

func TestPrintDeprecatedAliasNotice(t *testing.T) {
	var buf bytes.Buffer
	printDeprecatedAliasNotice(&buf, "old", "new")
	got := buf.String()
	if !strings.Contains(got, `"old"`) || !strings.Contains(got, `"new"`) || !strings.Contains(got, "deprecated alias") {
		t.Fatalf("printDeprecatedAliasNotice = %q", got)
	}
}

func TestPrintProxyUsage(t *testing.T) {
	var buf bytes.Buffer
	printProxyUsage(&buf)
	got := buf.String()
	for _, want := range []string{"cove run -proxy", "Linux", "macOS", "Preflight"} {
		if !strings.Contains(got, want) {
			t.Fatalf("printProxyUsage missing %q in:\n%s", want, got)
		}
	}
}

func TestPrintVMConfigUsage(t *testing.T) {
	var buf bytes.Buffer
	printVMConfigUsage(&buf)
	got := buf.String()
	for _, want := range []string{"cove vm config", "export", "import"} {
		if !strings.Contains(got, want) {
			t.Fatalf("printVMConfigUsage missing %q", want)
		}
	}
}

func TestPrintForkUsage(t *testing.T) {
	var buf bytes.Buffer
	printForkUsage(&buf)
	got := buf.String()
	for _, want := range []string{"cove fork", "--from", "-snapshot", "APFS"} {
		if !strings.Contains(got, want) {
			t.Fatalf("printForkUsage missing %q", want)
		}
	}
}
