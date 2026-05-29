package main

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestElevationPrompt(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"short", "stage files", "stage files"},
		{"empty", "", ""},
		{"exact_140", strings.Repeat("a", 140), strings.Repeat("a", 140)},
		{"truncates_over_140", strings.Repeat("b", 200), strings.Repeat("b", 139) + "…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := elevationPrompt(tt.in)
			if got != tt.want {
				t.Errorf("elevationPrompt(%d chars) = %q, want %q", len(tt.in), got, tt.want)
			}
		})
	}
}

func TestRestrictedEnvironment(t *testing.T) {
	t.Setenv("CLAUDECODE", "")
	t.Setenv("IS_SANDBOX", "")
	t.Setenv("COVE_FORCE_MANUAL_ELEVATION", "")
	if restrictedEnvironment() {
		t.Fatal("baseline: want false with all signals cleared")
	}
	for _, key := range []string{"CLAUDECODE", "IS_SANDBOX", "COVE_FORCE_MANUAL_ELEVATION"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv("CLAUDECODE", "")
			t.Setenv("IS_SANDBOX", "")
			t.Setenv("COVE_FORCE_MANUAL_ELEVATION", "")
			t.Setenv(key, "1")
			if !restrictedEnvironment() {
				t.Errorf("%s=1 did not flag restricted", key)
			}
			t.Setenv(key, "0")
			if restrictedEnvironment() {
				t.Errorf("%s=0 should not flag restricted", key)
			}
		})
	}
}

func TestTmpPathFor(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	p := tmpPathFor("vz-test-")
	if p == "" {
		t.Fatal("empty path")
	}
	if !strings.Contains(filepath.Base(p), "vz-test-") {
		t.Errorf("path %q missing prefix", p)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("path should not exist on disk; stat err=%v", err)
	}
	q := tmpPathFor("vz-test-")
	if p == q {
		t.Errorf("expected unique paths, got %q twice", p)
	}
}

func TestPrintVerifyUsage(t *testing.T) {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.Bool("v", false, "Verbose output")
	fs.Bool("fix", false, "Attempt to fix issues automatically")
	var buf bytes.Buffer
	printVerifyUsage(&buf, fs)
	out := buf.String()
	for _, want := range []string{"Usage: cove doctor", "--fix", "tcc-path", "Examples:"} {
		if !strings.Contains(out, want) {
			t.Errorf("verify usage missing %q\n%s", want, out)
		}
	}
}

func TestPrintInjectUsage(t *testing.T) {
	fs := flag.NewFlagSet("provision", flag.ContinueOnError)
	fs.String("user", "", "username")
	var buf bytes.Buffer
	printInjectUsage(&buf, fs)
	out := buf.String()
	for _, want := range []string{
		"Usage: cove provision",
		"Two-phase provisioning",
		"LaunchDaemon mode",
		"-stage-only",
		"Examples:",
		"-no-bootstrap-recovery",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("inject usage missing %q", want)
		}
	}
}

func TestNewVerifyFlagSet(t *testing.T) {
	fs, verbose, fix, tccPath, vm := newVerifyFlagSet()
	if fs == nil {
		t.Fatal("nil flagset")
	}
	if err := fs.Parse([]string{"-v", "-fix", "-tcc-path", "/Volumes/work", "-vm", "myvm"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !*verbose {
		t.Error("verbose flag not set")
	}
	if !*fix {
		t.Error("fix flag not set")
	}
	if *tccPath != "/Volumes/work" {
		t.Errorf("tcc-path = %q", *tccPath)
	}
	if *vm != "myvm" {
		t.Errorf("vm = %q", *vm)
	}
}
