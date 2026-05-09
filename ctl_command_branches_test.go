package main

import (
	"strings"
	"testing"
)

// TestCtlCommandEarlyBranches covers the dispatch paths that exit before
// any control socket is dialed: help args, flag-parse errors, and the
// missing-subcommand usage error.
func TestCtlCommandEarlyBranches(t *testing.T) {
	t.Run("help alias returns nil", func(t *testing.T) {
		for _, alias := range []string{"help", "-h", "--help"} {
			if err := ctlCommand([]string{alias}); err != nil {
				t.Errorf("ctlCommand(%q) = %v, want nil", alias, err)
			}
		}
	})

	t.Run("unknown flag returns parse error", func(t *testing.T) {
		err := ctlCommand([]string{"-not-a-real-ctl-flag"})
		if err == nil {
			t.Fatal("ctlCommand bogus flag: got nil, want parse error")
		}
	})

	t.Run("missing subcommand returns command-required", func(t *testing.T) {
		err := ctlCommand([]string{"-socket", "/tmp/nonexistent.sock"})
		if err == nil || !strings.Contains(err.Error(), "command required") {
			t.Fatalf("ctlCommand no subcmd: got %v, want 'command required'", err)
		}
	})
}
