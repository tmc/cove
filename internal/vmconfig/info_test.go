package vmconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInfoFor(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "linux-disk.img"), []byte("disk"), 0644); err != nil {
		t.Fatalf("WriteFile(linux-disk.img) error = %v", err)
	}
	got, err := InfoFor(dir, func(string) string { return "running" })
	if err != nil {
		t.Fatalf("InfoFor() error = %v", err)
	}
	if got.Name != filepath.Base(dir) || got.Path != dir || got.DiskSize != 4 || got.State != "running" || got.OSType != "Linux" {
		t.Fatalf("InfoFor() = %#v", got)
	}
}

func TestInfoForWindows(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "windows-disk.img"), []byte("disk"), 0644); err != nil {
		t.Fatalf("WriteFile(windows-disk.img) error = %v", err)
	}
	got, err := InfoFor(dir, nil)
	if err != nil {
		t.Fatalf("InfoFor() error = %v", err)
	}
	if got.OSType != "Windows" || got.DiskSize != 4 {
		t.Fatalf("InfoFor() = %#v", got)
	}
}

func TestInfoForDefaultState(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "disk.img"), []byte("disk"), 0644); err != nil {
		t.Fatalf("WriteFile(disk.img) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "aux.img"), []byte("aux"), 0644); err != nil {
		t.Fatalf("WriteFile(aux.img) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "suspend.vmstate"), []byte("state"), 0644); err != nil {
		t.Fatalf("WriteFile(suspend.vmstate) error = %v", err)
	}
	got, err := InfoFor(dir, nil)
	if err != nil {
		t.Fatalf("InfoFor() error = %v", err)
	}
	if got.State != "suspended" {
		t.Fatalf("InfoFor().State = %q, want suspended", got.State)
	}
}

func TestList(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, name := range []string{"b", "a"} {
		dir := filepath.Join(BaseDir(), name)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "linux-disk.img"), []byte("disk"), 0644); err != nil {
			t.Fatalf("WriteFile(%s/linux-disk.img) error = %v", name, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(BaseDir(), "orphan"), 0755); err != nil {
		t.Fatalf("MkdirAll(orphan) error = %v", err)
	}

	got, err := List(nil)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "b" {
		t.Fatalf("List() = %#v", got)
	}
	for _, name := range []string{"a", "b"} {
		alias := filepath.Join(BundleDir(), name+".covevm")
		if !Validate(alias) {
			t.Fatalf("package alias %q is not a valid VM", alias)
		}
	}
}

func TestListDoesNotIncludePackageAliases(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := filepath.Join(BaseDir(), "dev")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll(dev) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "linux-disk.img"), []byte("disk"), 0644); err != nil {
		t.Fatalf("WriteFile(linux-disk.img) error = %v", err)
	}

	first, err := List(nil)
	if err != nil {
		t.Fatalf("List() first error = %v", err)
	}
	if len(first) != 1 || first[0].Name != "dev" {
		t.Fatalf("List() first = %#v, want dev", first)
	}

	second, err := List(nil)
	if err != nil {
		t.Fatalf("List() second error = %v", err)
	}
	if len(second) != 1 || second[0].Name != "dev" {
		t.Fatalf("List() second = %#v, want dev only", second)
	}
}

// TestListFollowsSymlinks ensures alias symlinks created by EnsureAlias for
// VMs resolved outside BaseDir (legacy ~/.vz/<name> layout) are visible to
// List() — the migration code plants symlinks here on every name resolution.
func TestListFollowsSymlinks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	legacyDir := filepath.Join(home, ".vz", "legacy-vm")
	if err := os.MkdirAll(legacyDir, 0755); err != nil {
		t.Fatalf("MkdirAll(legacy) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "linux-disk.img"), []byte("disk"), 0644); err != nil {
		t.Fatalf("WriteFile(legacy/linux-disk.img) error = %v", err)
	}

	if err := os.MkdirAll(BaseDir(), 0755); err != nil {
		t.Fatalf("MkdirAll(BaseDir) error = %v", err)
	}
	if err := os.Symlink(legacyDir, filepath.Join(BaseDir(), "legacy-vm")); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	got, err := List(nil)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 1 || got[0].Name != "legacy-vm" {
		t.Fatalf("List() = %#v, want one entry named legacy-vm", got)
	}
}

// TestListDiscoversLegacyLayout ensures List() finds VMs under the legacy
// ~/.vz/<name>/ layout even when no alias has been planted under BaseDir,
// and plants the alias as a side effect so subsequent lists work via the
// normal BaseDir scan.
func TestListDiscoversLegacyLayout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	for _, name := range []string{"legacy-mac", "legacy-linux"} {
		dir := filepath.Join(home, ".vz", name)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", name, err)
		}
		if name == "legacy-mac" {
			for _, f := range []string{"disk.img", "aux.img"} {
				if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0644); err != nil {
					t.Fatalf("WriteFile(%s) error = %v", f, err)
				}
			}
		} else {
			if err := os.WriteFile(filepath.Join(dir, "linux-disk.img"), []byte("x"), 0644); err != nil {
				t.Fatalf("WriteFile(linux-disk.img) error = %v", err)
			}
		}
	}

	got, err := List(nil)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 2 || got[0].Name != "legacy-linux" || got[1].Name != "legacy-mac" {
		t.Fatalf("List() = %#v, want both legacy VMs", got)
	}
	for _, name := range []string{"legacy-mac", "legacy-linux"} {
		alias := filepath.Join(BaseDir(), name)
		info, err := os.Lstat(alias)
		if err != nil {
			t.Fatalf("alias %q not planted: %v", name, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("alias %q not a symlink (mode %v)", name, info.Mode())
		}
	}
}
