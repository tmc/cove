package vmconfig

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/cove/internal/mutationguard"
)

func TestVMConfigMutationsRespectRootGuard(t *testing.T) {
	t.Setenv(StateDirEnv, t.TempDir())
	guard, err := mutationguard.Acquire(StateDir())
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Release()
	tests := []struct {
		name string
		run  func() error
	}{
		{"migration", MigrateIfNeeded},
		{"package", func() error { _, err := EnsurePackageLayout("legacy", filepath.Join(BaseDir(), "legacy")); return err }},
		{"listing", func() error { _, err := List(nil); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(); !errors.Is(err, mutationguard.ErrBusy) {
				t.Fatalf("bypassed guard: %v", err)
			}
		})
	}
	if err := MigrateIfNeededWithGuard(guard); err != nil {
		t.Fatal(err)
	}
	dir, err := EnsureDirWithGuard("guarded", "", guard)
	if err != nil {
		t.Fatal(err)
	}
	if dir == "" {
		t.Fatal("missing created directory")
	}
	if _, err := EnsurePackageLayoutWithGuard("guarded", dir, guard); err != nil {
		t.Fatal(err)
	}
	other, err := mutationguard.Acquire(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Release()
	if _, err := EnsureDirWithGuard("wrong", "", other); err == nil {
		t.Fatal("accepted wrong-root guard")
	}
	if _, err := EnsureDirWithGuard("wrong", "", nil); err == nil {
		t.Fatal("accepted nil guard")
	}
}

func TestListReadOnlyDoesNotMigrateUnderGuard(t *testing.T) {
	t.Setenv(StateDirEnv, t.TempDir())
	legacy := filepath.Join(BaseDir(), "legacy")
	if err := os.MkdirAll(legacy, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "linux-disk.img"), []byte("disk"), 0600); err != nil {
		t.Fatal(err)
	}
	guard, err := mutationguard.Acquire(StateDir())
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Release()
	infos, err := ListReadOnly(nil)
	if err != nil || len(infos) != 1 {
		t.Fatalf("infos %v, error %v", infos, err)
	}
	if _, err := os.Lstat(legacy); err != nil {
		t.Fatal("read-only listing moved legacy directory")
	}
	if _, err := os.Lstat(filepath.Join(BaseDir(), "legacy.covevm")); !os.IsNotExist(err) {
		t.Fatalf("created package: %v", err)
	}
	if _, err := os.Lstat(BundleDir()); !os.IsNotExist(err) {
		t.Fatalf("created Finder aliases: %v", err)
	}
	guard.Release()
	infos, err = List(nil)
	if err != nil || len(infos) != 1 {
		t.Fatalf("mutable listing %v, error %v", infos, err)
	}
	if _, err := os.Stat(filepath.Join(BaseDir(), "legacy.covevm")); err != nil {
		t.Fatal(err)
	}
}
