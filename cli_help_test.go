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

func TestPrintSharedFolderUsageDocumentsLiveApply(t *testing.T) {
	var buf bytes.Buffer
	printSharedFolderUsage(&buf)
	got := buf.String()
	for _, want := range []string{"Save and live-apply when running", "Retry guest mount via agent"} {
		if !strings.Contains(got, want) {
			t.Fatalf("printSharedFolderUsage missing %q:\n%s", want, got)
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

func TestHandleEarlyCLIProductHelpTopics(t *testing.T) {
	for _, tc := range []struct {
		topic string
		want  string
	}{
		{"action", "Usage: cove action"},
		{"runs", "Usage: cove runs"},
		{"daemon", "Usage: cove daemon"},
		{"cp", "Usage: cove cp"},
		{"forward", "Usage: cove forward"},
		{"quota", "Usage: cove quota"},
		{"diff", "Usage: cove diff"},
		{"image", "Usage: cove image"},
		{"logs", "Usage: cove logs"},
		{"security", "Usage: cove security"},
	} {
		t.Run(tc.topic, func(t *testing.T) {
			stderr, restore := captureStderr(t)
			handled, code := handleEarlyCLI([]string{"help", tc.topic})
			restore()
			if !handled || code != 0 {
				t.Fatalf("handleEarlyCLI(help %s) = handled %v code %d, want true 0", tc.topic, handled, code)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("help %s output missing %q:\n%s", tc.topic, tc.want, stderr.String())
			}
		})
	}
}

func TestHandleEarlyCLINoArgProductSurfaces(t *testing.T) {
	for _, tc := range []struct {
		cmd  string
		want string
	}{
		{"runs", "Usage: cove runs"},
		{"action", "Usage: cove action"},
		{"daemon", "Usage: cove daemon"},
		{"cp", "Usage: cove cp"},
		{"forward", "Usage: cove forward"},
		{"quota", "Usage: cove quota"},
		{"diff", "Usage: cove diff"},
		{"image", "Usage: cove image"},
		{"logs", "Usage: cove logs"},
		{"security", "Usage: cove security"},
	} {
		t.Run(tc.cmd, func(t *testing.T) {
			stderr, restore := captureStderr(t)
			handled, code := handleEarlyCLI([]string{tc.cmd})
			restore()
			if !handled || code != 2 {
				t.Fatalf("handleEarlyCLI(%s) = handled %v code %d, want true 2", tc.cmd, handled, code)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("%s no-arg output missing %q:\n%s", tc.cmd, tc.want, stderr.String())
			}
		})
	}
}
