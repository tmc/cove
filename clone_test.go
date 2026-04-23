package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSupportsClonefile(t *testing.T) {
	dir := t.TempDir()

	// TempDir on macOS is typically APFS, so clonefile should work.
	got := SupportsClonefile(dir)
	if !got {
		t.Logf("clonefile not supported in %s (may be non-APFS)", dir)
	}
}

func TestSupportsClonefileBadDir(t *testing.T) {
	if SupportsClonefile("/nonexistent/dir") {
		t.Error("expected false for nonexistent directory")
	}
}

func TestCloneFileCreatesIdenticalContent(t *testing.T) {
	dir := t.TempDir()

	src := dir + "/src.txt"
	dst := dir + "/dst.txt"
	content := []byte("hello clonefile")
	if err := os.WriteFile(src, content, 0644); err != nil {
		t.Fatal(err)
	}

	if err := cloneFile(src, dst); err != nil {
		t.Skipf("clonefile not available: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Errorf("clone content = %q, want %q", got, content)
	}
}

func TestCloneVMLinux(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	src := filepath.Join(GetVMBaseDir(), "src-linux")
	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"linux-disk.img":   []byte("disk"),
		"linux-machine.id": []byte("machine"),
		"config.json":      []byte("{\"memoryGB\":4}\n"),
		"control.token":    []byte("token"),
	} {
		if err := os.WriteFile(filepath.Join(src, name), data, 0644); err != nil {
			t.Fatal(err)
		}
	}

	if err := CloneVM(CloneOptions{
		Source:        "src-linux",
		Target:        "dst-linux",
		CopyMachineID: true,
	}); err != nil {
		t.Fatalf("CloneVM() error = %v", err)
	}

	dst := filepath.Join(GetVMBaseDir(), "dst-linux")
	for _, name := range []string{"linux-disk.img", "linux-machine.id", "config.json", "control.token"} {
		if _, err := os.Stat(filepath.Join(dst, name)); err != nil {
			t.Fatalf("cloned file %q missing: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dst, "disk.img")); !os.IsNotExist(err) {
		t.Fatalf("unexpected macOS disk clone artifact: %v", err)
	}
}

func TestCloneVMUsesSourceDiskOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	src := filepath.Join(GetVMBaseDir(), "src-macos")
	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"disk.img":    []byte("live-disk"),
		"aux.img":     []byte("aux"),
		"hw.model":    []byte("hw"),
		"machine.id":  []byte("machine"),
		"config.json": []byte("{\"memoryGB\":4}\n"),
	} {
		if err := os.WriteFile(filepath.Join(src, name), data, 0644); err != nil {
			t.Fatal(err)
		}
	}

	snapshotDir := filepath.Join(src, "disk-snapshots", "clean-base")
	if err := os.MkdirAll(snapshotDir, 0755); err != nil {
		t.Fatal(err)
	}
	snapshotDisk := filepath.Join(snapshotDir, "disk.img")
	if err := os.WriteFile(snapshotDisk, []byte("snapshot-disk"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := CloneVM(CloneOptions{
		Source:         "src-macos",
		Target:         "dst-macos",
		CopyMachineID:  true,
		SourceDiskPath: snapshotDisk,
	}); err != nil {
		t.Fatalf("CloneVM() error = %v", err)
	}

	dst := filepath.Join(GetVMBaseDir(), "dst-macos")
	got, err := os.ReadFile(filepath.Join(dst, "disk.img"))
	if err != nil {
		t.Fatalf("ReadFile(dst disk): %v", err)
	}
	if string(got) != "snapshot-disk" {
		t.Fatalf("cloned disk = %q, want %q", got, "snapshot-disk")
	}
}
