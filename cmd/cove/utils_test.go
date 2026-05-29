package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHintEntitlements(t *testing.T) {
	base := errors.New("catalog failed to load: boom")
	tests := []struct {
		name     string
		err      error
		wantHint bool
	}{
		{"catalog", errors.New("catalog failed to load"), true},
		{"installation", errors.New("installation service crashed"), true},
		{"unexpected", errors.New("unexpected error: 1234"), true},
		{"benign", errors.New("permission denied"), false},
		{"wrapped catalog", base, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hintEntitlements(tt.err)
			if !errors.Is(got, tt.err) {
				t.Fatalf("errors.Is broken: %v", got)
			}
			has := strings.Contains(got.Error(), "entitlement")
			if has != tt.wantHint {
				t.Fatalf("hint=%v want %v: %q", has, tt.wantHint, got)
			}
		})
	}
}

func TestResolvePath(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	realDir, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := resolvePath(link); got != realDir {
		t.Errorf("resolvePath(link) = %q, want %q", got, realDir)
	}
	// Non-existent path: returns absolute path unchanged.
	missing := filepath.Join(dir, "does-not-exist")
	if got := resolvePath(missing); got != missing {
		t.Errorf("resolvePath(missing) = %q, want %q", got, missing)
	}
}
