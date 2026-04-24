package vmconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestValidate(t *testing.T) {
	t.Run("macos", func(t *testing.T) {
		dir := t.TempDir()
		for _, name := range RequiredFiles {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0644); err != nil {
				t.Fatalf("WriteFile(%s) error = %v", name, err)
			}
		}
		if !Validate(dir) {
			t.Fatal("Validate() = false, want true")
		}
	})
	t.Run("linux", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "linux-disk.img"), []byte("disk"), 0644); err != nil {
			t.Fatalf("WriteFile(linux-disk.img) error = %v", err)
		}
		if !Validate(dir) {
			t.Fatal("Validate() = false, want true")
		}
	})
	t.Run("invalid", func(t *testing.T) {
		if Validate(t.TempDir()) {
			t.Fatal("Validate() = true, want false")
		}
	})
}

func TestActiveName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if got := ActiveName(); got != "default" {
		t.Fatalf("ActiveName() = %q, want default", got)
	}
	vmPath := filepath.Join(BaseDir(), "dev")
	if err := os.MkdirAll(vmPath, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	for _, name := range RequiredFiles {
		if err := os.WriteFile(filepath.Join(vmPath, name), []byte(name), 0644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(CurrentLink()), 0755); err != nil {
		t.Fatalf("MkdirAll(current parent) error = %v", err)
	}
	if err := SetActive("dev"); err != nil {
		t.Fatalf("SetActive() error = %v", err)
	}
	if got := ActiveName(); got != "dev" {
		t.Fatalf("ActiveName() = %q, want dev", got)
	}
	if err := UnsetActive(); err != nil {
		t.Fatalf("UnsetActive() error = %v", err)
	}
	if got := ActiveName(); got != "default" {
		t.Fatalf("ActiveName() after unset = %q, want default", got)
	}
}

func TestListOrphans(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(BaseDir(), "valid"), 0755); err != nil {
		t.Fatalf("MkdirAll(valid) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(BaseDir(), "valid", "linux-disk.img"), []byte("disk"), 0644); err != nil {
		t.Fatalf("WriteFile(linux-disk.img) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(BaseDir(), "orphan-b"), 0755); err != nil {
		t.Fatalf("MkdirAll(orphan-b) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(BaseDir(), "orphan-a"), 0755); err != nil {
		t.Fatalf("MkdirAll(orphan-a) error = %v", err)
	}

	got, err := ListOrphans()
	if err != nil {
		t.Fatalf("ListOrphans() error = %v", err)
	}
	want := []string{"orphan-a", "orphan-b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListOrphans() = %#v, want %#v", got, want)
	}
}
