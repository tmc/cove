package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyScreenshotsMissingSrcReturnsNil(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "out")
	src := filepath.Join(t.TempDir(), "absent")
	if err := copyScreenshots(src, dst); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("dst dir not created: %v", err)
	}
}

func TestCopyScreenshotsCopiesPNGsAndSkipsOthers(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "out")
	for _, name := range []string{"a.png", "b.PNG", "c.txt"} {
		if err := os.WriteFile(filepath.Join(src, name), []byte("data-"+name), 0644); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}
	if err := os.Mkdir(filepath.Join(src, "nested"), 0755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	if err := copyScreenshots(src, dst); err != nil {
		t.Fatalf("err = %v", err)
	}
	for _, name := range []string{"a.png", "b.PNG"} {
		if _, err := os.Stat(filepath.Join(dst, name)); err != nil {
			t.Fatalf("missing %s in dst: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dst, "c.txt")); !os.IsNotExist(err) {
		t.Fatalf("c.txt was copied but should have been skipped")
	}
	if _, err := os.Stat(filepath.Join(dst, "nested")); !os.IsNotExist(err) {
		t.Fatalf("nested dir was copied but should have been skipped")
	}
}
