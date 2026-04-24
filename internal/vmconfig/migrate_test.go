package vmconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateIfNeeded(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(BaseDir(), 0755); err != nil {
		t.Fatalf("MkdirAll(BaseDir()) error = %v", err)
	}
	for _, name := range append(Files, "boot-args.txt") {
		if err := os.WriteFile(filepath.Join(BaseDir(), name), []byte(name), 0644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(BaseDir(), "RestoreImage.ipsw"), []byte("ipsw"), 0644); err != nil {
		t.Fatalf("WriteFile(RestoreImage.ipsw) error = %v", err)
	}

	if err := MigrateIfNeeded(); err != nil {
		t.Fatalf("MigrateIfNeeded() error = %v", err)
	}

	defaultDir := filepath.Join(BaseDir(), "default")
	for _, name := range append(Files, "boot-args.txt") {
		if _, err := os.Stat(filepath.Join(defaultDir, name)); err != nil {
			t.Fatalf("Stat(default/%s) error = %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(CacheDir(), "RestoreImage.ipsw")); err != nil {
		t.Fatalf("Stat(cache/RestoreImage.ipsw) error = %v", err)
	}
	if got := ActiveName(); got != "default" {
		t.Fatalf("ActiveName() = %q, want default", got)
	}
}

func TestEnsureAlias(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	legacyPath := filepath.Join(filepath.Dir(BaseDir()), "legacy")
	if err := os.MkdirAll(legacyPath, 0755); err != nil {
		t.Fatalf("MkdirAll(legacy) error = %v", err)
	}

	if err := EnsureAlias("legacy", legacyPath); err != nil {
		t.Fatalf("EnsureAlias() error = %v", err)
	}
	link, err := os.Readlink(filepath.Join(BaseDir(), "legacy"))
	if err != nil {
		t.Fatalf("Readlink(alias) error = %v", err)
	}
	if link != resolvePath(legacyPath) {
		t.Fatalf("alias = %q, want %q", link, resolvePath(legacyPath))
	}
}
