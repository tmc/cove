package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunElevatedManifestVerifiesEveryRootOwnedTargetE2E(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to create ownership fixtures")
	}

	dir := t.TempDir()
	first := filepath.Join(dir, "first")
	second := filepath.Join(dir, "second")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	if err := os.Chown(first, 0, 0); err != nil {
		t.Fatalf("chown first: %v", err)
	}
	if err := os.Chown(second, 501, 20); err != nil {
		t.Fatalf("chown second: %v", err)
	}

	err := runElevatedManifest(&elevatedManifest{
		VerifyChownTargets: []string{first, second},
	})
	if err == nil || !strings.Contains(err.Error(), second) {
		t.Fatalf("runElevatedManifest error = %v, want second target ownership failure", err)
	}

	if err := os.Chown(second, 0, 0); err != nil {
		t.Fatalf("repair second: %v", err)
	}
	if err := runElevatedManifest(&elevatedManifest{
		VerifyChownTargets: []string{first, second},
	}); err != nil {
		t.Fatalf("runElevatedManifest after repair: %v", err)
	}
}
