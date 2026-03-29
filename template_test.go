package main

import "testing"

func TestProvisioningSourceHash(t *testing.T) {
	h1 := ProvisioningSourceHash()
	if len(h1) != 12 {
		t.Fatalf("hash length = %d, want 12", len(h1))
	}

	// Hash should be deterministic.
	h2 := ProvisioningSourceHash()
	if h1 != h2 {
		t.Errorf("hash not deterministic: %q != %q", h1, h2)
	}
}

func TestCheckTemplateStaleNoHash(t *testing.T) {
	dir := t.TempDir()
	stale, tmplHash, curHash := CheckTemplateStale(dir)
	if stale {
		t.Error("expected not stale when no hash file exists")
	}
	if tmplHash != "" || curHash != "" {
		t.Errorf("expected empty hashes, got tmpl=%q cur=%q", tmplHash, curHash)
	}
}
