package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRotateMasterTokenWritesFile covers the happy path of RotateMasterToken
// with an explicit tokenFile: a fresh token is generated, written to the
// requested path with 0600 perms, and replaces any prior contents.
func TestRotateMasterTokenWritesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "control.token")
	if err := os.WriteFile(path, []byte("stale-token"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	tok, err := RotateMasterToken(path)
	if err != nil {
		t.Fatalf("RotateMasterToken: %v", err)
	}
	assertTokenFormat(t, tok)

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read token file: %v", err)
	}
	if string(got) != tok+"\n" && string(got) != tok {
		t.Fatalf("token file content = %q, want %q", got, tok)
	}
	if string(got) == "stale-token" {
		t.Fatal("token file still contains stale value")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("token file perms = %04o, want 0600", perm)
	}
}

// TestRotateMasterTokenGeneratesUniqueValues confirms two consecutive
// rotations produce distinct tokens; protects against any future caching
// regression in the generator path.
func TestRotateMasterTokenGeneratesUniqueValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "control.token")

	first, err := RotateMasterToken(path)
	if err != nil {
		t.Fatalf("first rotate: %v", err)
	}
	second, err := RotateMasterToken(path)
	if err != nil {
		t.Fatalf("second rotate: %v", err)
	}
	if first == second {
		t.Fatalf("rotate produced identical tokens twice: %q", first)
	}
}
