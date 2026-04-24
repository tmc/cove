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
}
