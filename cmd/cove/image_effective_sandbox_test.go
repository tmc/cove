package main

import "testing"

func TestEffectiveSandboxMode(t *testing.T) {
	prev := sandboxLevel
	t.Cleanup(func() { sandboxLevel = prev })

	sandboxLevel = ""
	if got := effectiveSandboxMode(); got != "default" {
		t.Fatalf("empty = %q, want default", got)
	}

	sandboxLevel = "  minimal  "
	if got := effectiveSandboxMode(); got != "minimal" {
		t.Fatalf("trimmed = %q, want minimal", got)
	}

	sandboxLevel = "   "
	if got := effectiveSandboxMode(); got != "default" {
		t.Fatalf("whitespace-only = %q, want default", got)
	}
}
