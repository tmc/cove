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

func TestEnsurePackageAlias(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := filepath.Join(BaseDir(), "dev")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll(dev) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "linux-disk.img"), []byte("disk"), 0644); err != nil {
		t.Fatalf("WriteFile(linux-disk.img) error = %v", err)
	}

	if err := EnsurePackageAlias("dev", dir); err != nil {
		t.Fatalf("EnsurePackageAlias() error = %v", err)
	}
	alias := PackageAliasPath("dev")
	link, err := os.Readlink(alias)
	if err != nil {
		t.Fatalf("Readlink(alias) error = %v", err)
	}
	if link != resolvePath(dir) {
		t.Fatalf("alias = %q, want %q", link, resolvePath(dir))
	}
	if !Validate(alias) {
		t.Fatal("Validate(package alias) = false, want true")
	}
}

func TestEnsurePackageAliasDoesNotDoubleExtension(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := filepath.Join(BaseDir(), "dev.covevm")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll(dev.covevm) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "linux-disk.img"), []byte("disk"), 0644); err != nil {
		t.Fatalf("WriteFile(linux-disk.img) error = %v", err)
	}

	if err := EnsurePackageAlias("dev.covevm", dir); err != nil {
		t.Fatalf("EnsurePackageAlias() error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(BundleDir(), "dev.covevm.covevm")); !os.IsNotExist(err) {
		t.Fatalf("double-extension alias exists: %v", err)
	}
	if !Validate(PackageAliasPath("dev.covevm")) {
		t.Fatal("package alias is not a valid VM")
	}
}

func TestEnsurePackageAliasReplacesStaleSymlink(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldDir := filepath.Join(t.TempDir(), "old")
	newDir := filepath.Join(BaseDir(), "dev")
	if err := os.MkdirAll(oldDir, 0755); err != nil {
		t.Fatalf("MkdirAll(old) error = %v", err)
	}
	if err := os.MkdirAll(newDir, 0755); err != nil {
		t.Fatalf("MkdirAll(new) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(newDir, "linux-disk.img"), []byte("disk"), 0644); err != nil {
		t.Fatalf("WriteFile(linux-disk.img) error = %v", err)
	}
	if err := os.MkdirAll(BundleDir(), 0755); err != nil {
		t.Fatalf("MkdirAll(BundleDir) error = %v", err)
	}
	alias := PackageAliasPath("dev")
	if err := os.Symlink(oldDir, alias); err != nil {
		t.Fatalf("Symlink(old) error = %v", err)
	}

	if err := EnsurePackageAlias("dev", newDir); err != nil {
		t.Fatalf("EnsurePackageAlias() error = %v", err)
	}
	link, err := os.Readlink(alias)
	if err != nil {
		t.Fatalf("Readlink(alias) error = %v", err)
	}
	if link != resolvePath(newDir) {
		t.Fatalf("alias = %q, want %q", link, resolvePath(newDir))
	}
}

func TestRemovePackageAlias(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := filepath.Join(BaseDir(), "dev")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll(dev) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "linux-disk.img"), []byte("disk"), 0644); err != nil {
		t.Fatalf("WriteFile(linux-disk.img) error = %v", err)
	}
	if err := EnsurePackageAlias("dev", dir); err != nil {
		t.Fatalf("EnsurePackageAlias() error = %v", err)
	}
	if err := RemovePackageAlias("dev"); err != nil {
		t.Fatalf("RemovePackageAlias() error = %v", err)
	}
	if _, err := os.Lstat(PackageAliasPath("dev")); !os.IsNotExist(err) {
		t.Fatalf("package alias still exists: %v", err)
	}
}
