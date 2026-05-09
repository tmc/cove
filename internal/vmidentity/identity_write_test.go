package vmidentity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteRejectsInvalidIdentities(t *testing.T) {
	tests := []struct {
		name string
		id   *Identity
		want string
	}{
		{name: "nil identity", id: nil, want: "nil identity"},
		{name: "empty machine id", id: &Identity{AuxPath: "/tmp/aux"}, want: "empty identity"},
		{name: "empty aux path", id: &Identity{MachineID: []byte("m")}, want: "empty source path"},
		{name: "whitespace aux path", id: &Identity{MachineID: []byte("m"), AuxPath: "   "}, want: "empty source path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Write(t.TempDir(), tt.id)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Write err=%v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestWriteRemovesStaleMACWhenMACEmpty(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	disk := filepath.Join(src, "disk.img")
	if err := os.WriteFile(filepath.Join(src, "aux.img"), []byte("aux"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(disk, []byte("disk"), 0644); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dst, "mac.address")
	if err := os.WriteFile(stale, []byte("00:11:22:33:44:55"), 0644); err != nil {
		t.Fatal(err)
	}
	id := &Identity{MachineID: []byte("m"), AuxPath: filepath.Join(src, "aux.img"), DiskPath: disk}
	if err := Write(dst, id); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale mac.address not removed: stat err=%v", err)
	}
}

func TestWriteSucceedsWhenMACFileAlreadyAbsent(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	disk := filepath.Join(src, "disk.img")
	if err := os.WriteFile(filepath.Join(src, "aux.img"), []byte("aux"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(disk, []byte("disk"), 0644); err != nil {
		t.Fatal(err)
	}
	id := &Identity{MachineID: []byte("m"), AuxPath: filepath.Join(src, "aux.img"), DiskPath: disk}
	if err := Write(dst, id); err != nil {
		t.Fatalf("Write: %v", err)
	}
}
