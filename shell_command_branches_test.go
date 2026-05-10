package main

import (
	"strings"
	"testing"
)

// TestShellCommandEarlyBranches covers the flag-parsing branches that exit
// before any socket dial: -h returns nil, an unknown flag surfaces a parse
// error from flag.Parse, and a missing positional VM argument returns the
// "vm name required" error.
func TestShellCommandEarlyBranches(t *testing.T) {
	t.Run("help flag returns nil", func(t *testing.T) {
		for _, alias := range []string{"-h", "--help"} {
			if err := shellCommand([]string{alias}); err != nil {
				t.Fatalf("shellCommand(%q) = %v, want nil", alias, err)
			}
		}
	})

	t.Run("unknown flag returns parse error", func(t *testing.T) {
		err := shellCommand([]string{"-not-a-real-flag"})
		if err == nil {
			t.Fatalf("shellCommand unknown-flag = nil, want parse error")
		}
		if strings.Contains(err.Error(), "vm name required") {
			t.Fatalf("expected parse error, got vm-required: %v", err)
		}
	})

	t.Run("env flag without positional fails vm-required", func(t *testing.T) {
		err := shellCommand([]string{"-env", "FOO=bar"})
		if err == nil || !strings.Contains(err.Error(), "vm name required") {
			t.Fatalf("shellCommand(-env FOO=bar) = %v, want vm name required", err)
		}
	})
}
