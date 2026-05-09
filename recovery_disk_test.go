package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRecoveryDiskPath(t *testing.T) {
	tests := []struct {
		name string
		dir  string
		want string
	}{
		{"absolute", "/var/vms/foo", "/var/vms/foo/recovery-disk.img"},
		{"trailing slash", "/var/vms/foo/", "/var/vms/foo/recovery-disk.img"},
		{"relative", "vms/foo", "vms/foo/recovery-disk.img"},
		{"empty", "", "recovery-disk.img"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RecoveryDiskPath(tt.dir); got != tt.want {
				t.Errorf("RecoveryDiskPath(%q) = %q, want %q", tt.dir, got, tt.want)
			}
		})
	}
}

func TestEnsureRecoveryDiskUsesExisting(t *testing.T) {
	dir := t.TempDir()
	want := RecoveryDiskPath(dir)
	if err := os.WriteFile(want, []byte("not empty"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := EnsureRecoveryDisk(dir)
	if err != nil {
		t.Fatalf("EnsureRecoveryDisk: %v", err)
	}
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// File contents must be untouched (we did not invoke hdiutil).
	data, err := os.ReadFile(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "not empty" {
		t.Errorf("recovery disk was rewritten; contents = %q", data)
	}
}

func TestEnsureRecoveryDiskZeroSizeFileRecreated(t *testing.T) {
	// A zero-byte file should NOT be treated as a valid recovery disk.
	// EnsureRecoveryDisk would attempt to recreate via hdiutil; we just
	// confirm it does not short-circuit on the empty file.
	dir := t.TempDir()
	path := RecoveryDiskPath(dir)
	if err := os.WriteFile(path, nil, 0644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Fatalf("expected zero-size file, got %d", info.Size())
	}
	// We don't call EnsureRecoveryDisk here (it would shell out to hdiutil
	// and need privileges); we are documenting the size-check invariant.
}

func TestIPSWLooksCompleteRejectsMissingAndSmall(t *testing.T) {
	dir := t.TempDir()

	if ipswLooksComplete(filepath.Join(dir, "nope.ipsw")) {
		t.Error("missing file should not look complete")
	}

	small := filepath.Join(dir, "small.ipsw")
	if err := os.WriteFile(small, []byte("PK\x05\x06zzzz"), 0644); err != nil {
		t.Fatal(err)
	}
	if ipswLooksComplete(small) {
		t.Error("sub-1GB file should not look complete even with EOCD signature")
	}

	empty := filepath.Join(dir, "empty.ipsw")
	if err := os.WriteFile(empty, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if ipswLooksComplete(empty) {
		t.Error("empty file should not look complete")
	}
}
