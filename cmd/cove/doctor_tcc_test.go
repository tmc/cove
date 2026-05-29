package main

import (
	"io"
	"os"
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

func TestTCCAppleEventsProbeScript(t *testing.T) {
	script := tccAppleEventsProbeScript()
	for _, want := range []string{
		"kTCCServiceAppleEvents",
		"/usr/local/bin/vz-agent",
		"com.apple.Terminal",
		"auth_value=2",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("tccAppleEventsProbeScript missing %q:\n%s", want, script)
		}
	}
	if hint := captureAppleEventsHint(t); !strings.Contains(hint, "tccutil reset AppleEvents") {
		t.Fatalf("Apple Events hint missing reset command:\n%s", hint)
	}
}

func captureAppleEventsHint(t *testing.T) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	printAppleEventsHint()
	_ = w.Close()
	os.Stdout = old
	defer r.Close()
	var b strings.Builder
	if _, err := io.Copy(&b, r); err != nil {
		t.Fatal(err)
	}
	return b.String()
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
