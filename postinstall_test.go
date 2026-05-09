package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestRunPostInstallVZScriptsRejectsInvalidRecipe verifies the pre-flight
// validation returns an error before any VM boot is attempted.
func TestRunPostInstallVZScriptsRejectsInvalidRecipe(t *testing.T) {
	tests := []struct {
		name    string
		recipes string
		bad     string
	}{
		{"single missing", "does-not-exist-xyz", "does-not-exist-xyz"},
		{"trailing whitespace trimmed", "  does-not-exist-xyz  ", "does-not-exist-xyz"},
		{"second recipe missing", "homebrew,does-not-exist-xyz", "does-not-exist-xyz"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := runPostInstallVZScriptsWithOutput(tt.recipes, &buf)
			if err == nil {
				t.Fatalf("want error for recipes %q", tt.recipes)
			}
			if !strings.Contains(err.Error(), tt.bad) {
				t.Fatalf("error %q does not mention bad recipe %q", err, tt.bad)
			}
			if !strings.Contains(err.Error(), "recipe") {
				t.Fatalf("error %q missing recipe context", err)
			}
			// Pre-flight failure must not have started VM boot output.
			if strings.Contains(buf.String(), "Post-install: running") {
				t.Fatalf("validation should fail before progress banner; got:\n%s", buf.String())
			}
		})
	}
}
