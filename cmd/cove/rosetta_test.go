package main

import (
	"strings"
	"testing"
)

func TestRosettaAvailabilityString(t *testing.T) {
	tests := []struct {
		name string
		in   RosettaAvailability
		want string
	}{
		{"not supported", RosettaNotSupported, "not supported"},
		{"not installed", RosettaNotInstalled, "not installed"},
		{"installed", RosettaInstalled, "installed"},
		{"unknown", RosettaAvailability(99), "unknown (99)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.in.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRosettaHelpMentionsCommands(t *testing.T) {
	h := RosettaHelp()
	for _, want := range []string{"status", "install", "setup", "Apple Silicon"} {
		if !strings.Contains(h, want) {
			t.Errorf("RosettaHelp() missing %q", want)
		}
	}
}

func TestHandleRosettaCommandUnknown(t *testing.T) {
	err := handleRosettaCommand([]string{"bogus"})
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
	if !strings.Contains(err.Error(), "unknown rosetta command") {
		t.Errorf("unexpected error: %v", err)
	}
}
