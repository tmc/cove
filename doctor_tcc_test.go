package main

import (
	"strings"
	"testing"
)

func TestFirstOutputLine(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"blank", "\n \n", ""},
		{"first", "/Volumes/work\n/Volumes/cache\n", "/Volumes/work"},
		{"trim", " \n  /Volumes/work  \n", "/Volumes/work"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := firstOutputLine(tt.input); got != tt.want {
				t.Fatalf("firstOutputLine(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestTCCProbeScriptHasTimeout(t *testing.T) {
	script := tccProbeScript()
	for _, want := range []string{
		"/bin/ls -1",
		"timed out waiting for Full Disk Access approval",
		"exit 124",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("tccProbeScript missing %q:\n%s", want, script)
		}
	}
}

func TestTCCVolumeDiscoverySkipsSystemVolumes(t *testing.T) {
	script := tccVolumeDiscoveryScript()
	for _, want := range []string{
		`"Macintosh HD"`,
		`"Macintosh HD - Data"`,
		`"VZRECOVERY"`,
		`printf '%s\n' "$p"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("tccVolumeDiscoveryScript missing %q:\n%s", want, script)
		}
	}
}

func TestIsENOENTStderr(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"fda-blocked", "ls: /Users/me/Documents: Operation not permitted", false},
		{"enoent", "ls: /Volumes/missing: No such file or directory", true},
		{"enoent-mixed-case", "No Such File Or Directory", true},
		{"timeout", "timed out waiting for Full Disk Access approval", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isENOENTStderr(tt.in); got != tt.want {
				t.Fatalf("isENOENTStderr(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
