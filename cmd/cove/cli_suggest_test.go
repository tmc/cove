package main

import (
	"testing"

	"github.com/tmc/cove/internal/covecli"
)

func TestKnownCommandsCoverAliases(t *testing.T) {
	required := []string{
		"run", "install", "list", "ls", "shared-folder", "shared-folders",
		"vm", "rename", "export", "import", "config", "rm",
		"verify", "doctor", "provision", "inject", "first-run", "support-bundle",
	}
	set := make(map[string]bool, len(commandNames()))
	for _, k := range commandNames() {
		set[k] = true
	}
	for _, r := range required {
		if !set[r] {
			t.Errorf("knownCommands missing %q", r)
		}
	}
}

func TestLookupCommandAliases(t *testing.T) {
	tests := []struct {
		name      string
		wantName  string
		wantClass covecli.Dispatch
	}{
		{"run", "run", covecli.DispatchLate},
		{"ls", "list", covecli.DispatchLate},
		{"inject", "inject", covecli.DispatchEarly},
		{"doctor", "verify", covecli.DispatchEarly},
		{"shared-folders", "shared-folder", covecli.DispatchEarly},
		{"first-run", "first-run", covecli.DispatchEarly},
		{"support-bundle", "support-bundle", covecli.DispatchEarly},
		{"action", "action", covecli.DispatchPreUI},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec, ok := lookupCommand(tt.name)
			if !ok {
				t.Fatalf("lookupCommand(%q) not found", tt.name)
			}
			if spec.Name != tt.wantName || spec.Dispatch != tt.wantClass {
				t.Fatalf("lookupCommand(%q) = (%q, %v), want (%q, %v)", tt.name, spec.Name, spec.Dispatch, tt.wantName, tt.wantClass)
			}
		})
	}
}
