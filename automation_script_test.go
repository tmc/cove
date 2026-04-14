package main

import (
	"strings"
	"testing"
)

func TestIsVZScriptAutomationFile(t *testing.T) {
	tests := []struct {
		name string
		path string
		data string
		want bool
	}{
		{name: "vzscript extension", path: "sip-disable.vzscript", data: `wait 1s`, want: true},
		{name: "shell like content", path: "sip-disable.txt", data: "wait 1s\nocr-wait Options 60s\n", want: true},
		{name: "legacy boot commands", path: "sip-disable.txt", data: "<wait 1s>\n<startupOptions>\n", want: false},
		{name: "comments only", path: "notes.txt", data: "# comment\n\n", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isVZScriptAutomationFile(tt.path, []byte(tt.data)); got != tt.want {
				t.Fatalf("isVZScriptAutomationFile(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestUnsupportedAutomationScriptError(t *testing.T) {
	err := unsupportedAutomationScriptError("/tmp/script.txt")
	if err == nil {
		t.Fatal("nil error")
	}
	if got, want := err.Error(), "vzscript"; !strings.Contains(got, want) {
		t.Fatalf("error %q missing %q", got, want)
	}
	if got, want := err.Error(), "no longer supported"; !strings.Contains(got, want) {
		t.Fatalf("error %q missing %q", got, want)
	}
}
