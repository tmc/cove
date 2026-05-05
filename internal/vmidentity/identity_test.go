package vmidentity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadWriteRoundTrip(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	disk := filepath.Join(src, "disk.img")
	writeFile(t, filepath.Join(src, "machine.id"), "machine")
	writeFile(t, filepath.Join(src, "aux.img"), "aux")
	writeFile(t, filepath.Join(src, "mac.address"), "aa:bb:cc:dd:ee:ff\n")
	writeFile(t, disk, "disk")

	id, err := Read(src, disk)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if err := Write(dst, id); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := Read(dst, disk)
	if err != nil {
		t.Fatalf("Read dst: %v", err)
	}
	if !Equal(id, got) {
		t.Fatalf("identity mismatch: %#v != %#v", id, got)
	}
	if got := readFile(t, filepath.Join(dst, "machine.id")); got != "machine" {
		t.Fatalf("machine.id = %q", got)
	}
	if got := readFile(t, filepath.Join(dst, "aux.img")); got != "aux" {
		t.Fatalf("aux.img = %q", got)
	}
	if got := strings.TrimSpace(readFile(t, filepath.Join(dst, "mac.address"))); got != "aa:bb:cc:dd:ee:ff" {
		t.Fatalf("mac.address = %q", got)
	}
}

func TestReadRequiresCompleteIdentity(t *testing.T) {
	dir := t.TempDir()
	disk := filepath.Join(dir, "disk.img")
	writeFile(t, disk, "disk")
	writeFile(t, filepath.Join(dir, "aux.img"), "aux")
	if _, err := Read(dir, disk); err == nil || !strings.Contains(err.Error(), "machine.id") {
		t.Fatalf("Read missing machine.id error = %v", err)
	}
	writeFile(t, filepath.Join(dir, "machine.id"), "machine")
	if _, err := Read(dir, ""); err == nil || !strings.Contains(err.Error(), "disk") {
		t.Fatalf("Read missing disk path error = %v", err)
	}
}

func TestWriteRejectsMismatchedBundle(t *testing.T) {
	dst := t.TempDir()
	id := &Identity{
		MachineID: []byte("machine"),
		AuxPath:   filepath.Join(t.TempDir(), "missing-aux.img"),
		DiskPath:  filepath.Join(t.TempDir(), "disk.img"),
	}
	if err := Write(dst, id); err == nil || !strings.Contains(err.Error(), "copy aux.img") {
		t.Fatalf("Write error = %v, want copy aux.img", err)
	}
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
