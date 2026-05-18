package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

// stageParentVMForEphemeralFork writes a parent VM directory with the
// minimal files needed by SetupEphemeralFork: aux.img, hw.model, and a
// dummy disk.img (so vmconfig.Validate passes and the runtime path
// would have something to attach).
func stageParentVMForEphemeralFork(t *testing.T, parent string) string {
	t.Helper()
	parentDir := filepath.Join(vmconfig.BaseDir(), parent)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	files := map[string][]byte{
		"disk.img":    {0xDE, 0xAD, 0xBE, 0xEF},
		"aux.img":     []byte("parent-aux-bytes"),
		"hw.model":    []byte("parent-hw-model"),
		"machine.id":  []byte("PARENT-MACHINE-ID-VALUE-32BYTES!"),
		"mac.address": []byte("aa:bb:cc:dd:ee:ff\n"),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(parentDir, name), data, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return parentDir
}

func TestSetupEphemeralFork_HappyPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	parent := "eph-parent"
	parentDir := stageParentVMForEphemeralFork(t, parent)

	fixed := time.Date(2026, 5, 1, 12, 30, 45, 0, time.UTC)
	fork, err := SetupEphemeralFork(EphemeralForkOptions{
		Parent: parent,
		Now:    func() time.Time { return fixed },
	})
	if err != nil {
		t.Fatalf("SetupEphemeralFork: %v", err)
	}

	wantName := "eph-parent-eph-20260501-123045"
	if fork.Name != wantName {
		t.Errorf("fork.Name = %q, want %q", fork.Name, wantName)
	}
	if fork.Source != parent {
		t.Errorf("fork.Source = %q, want %q", fork.Source, parent)
	}
	if !fork.CreatedAt.Equal(fixed) {
		t.Errorf("fork.CreatedAt = %v, want %v", fork.CreatedAt, fixed)
	}

	for _, f := range []string{ephemeralSentinel, "aux.img", "hw.model"} {
		if _, err := os.Stat(filepath.Join(fork.Path, f)); err != nil {
			t.Errorf("child missing %s: %v", f, err)
		}
	}
	// Parent's disk.img must NOT be cloned into the child (Model B
	// uses RAM-overlay; the disk source is the parent's disk.img).
	if _, err := os.Stat(filepath.Join(fork.Path, "disk.img")); !os.IsNotExist(err) {
		t.Error("child unexpectedly has its own disk.img; ephemeral should reuse parent's")
	}

	gotAux, err := os.ReadFile(filepath.Join(fork.Path, "aux.img"))
	if err != nil {
		t.Fatalf("read child aux: %v", err)
	}
	wantAux, err := os.ReadFile(filepath.Join(parentDir, "aux.img"))
	if err != nil {
		t.Fatalf("read parent aux: %v", err)
	}
	if string(gotAux) != string(wantAux) {
		t.Error("child aux.img differs from parent aux.img")
	}
	if _, err := os.Stat(filepath.Join(fork.Path, "machine.id")); err == nil {
		t.Error("child unexpectedly has machine.id without PreserveIdentity")
	}
	if _, err := os.Stat(filepath.Join(fork.Path, "mac.address")); err == nil {
		t.Error("child unexpectedly has mac.address without PreserveIdentity")
	}

	cfg, err := vmconfig.Load(fork.Path)
	if err != nil {
		t.Fatalf("load child config: %v", err)
	}
	if cfg.ParentVM != parent {
		t.Errorf("child ParentVM = %q, want %q", cfg.ParentVM, parent)
	}
	if cfg.ForkedAt.IsZero() {
		t.Error("child ForkedAt is zero")
	}
}

func TestSetupEphemeralForkPreservesIdentity(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	parent := "eph-parent-identity"
	parentDir := stageParentVMForEphemeralFork(t, parent)

	fork, err := SetupEphemeralFork(EphemeralForkOptions{
		Parent:           parent,
		Name:             "identity-child",
		PreserveIdentity: true,
	})
	if err != nil {
		t.Fatalf("SetupEphemeralFork: %v", err)
	}
	for _, name := range []string{"machine.id", "mac.address", "aux.img"} {
		got, err := os.ReadFile(filepath.Join(fork.Path, name))
		if err != nil {
			t.Fatalf("read child %s: %v", name, err)
		}
		want, err := os.ReadFile(filepath.Join(parentDir, name))
		if err != nil {
			t.Fatalf("read parent %s: %v", name, err)
		}
		if string(got) != string(want) {
			t.Fatalf("child %s = %q, want parent %q", name, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(fork.Path, "disk.img")); !os.IsNotExist(err) {
		t.Error("child unexpectedly has its own disk.img; identity-preserving ephemeral should still use parent disk")
	}
}

func TestSetupEphemeralFork_ExplicitName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	parent := "eph-parent-named"
	stageParentVMForEphemeralFork(t, parent)

	fork, err := SetupEphemeralFork(EphemeralForkOptions{
		Parent: parent,
		Name:   "scratch-1",
	})
	if err != nil {
		t.Fatalf("SetupEphemeralFork: %v", err)
	}
	if fork.Name != "scratch-1" {
		t.Errorf("fork.Name = %q, want %q", fork.Name, "scratch-1")
	}
	if vmconfig.NameForPath(fork.Path) != "scratch-1" {
		t.Errorf("fork.Path name = %q, want %q", vmconfig.NameForPath(fork.Path), "scratch-1")
	}
}

func TestSetupEphemeralFork_RejectsBadArgs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	parent := "eph-parent-bad"
	stageParentVMForEphemeralFork(t, parent)

	cases := []struct {
		name string
		opts EphemeralForkOptions
		want string
	}{
		{"empty parent", EphemeralForkOptions{}, "parent VM name required"},
		{"missing parent", EphemeralForkOptions{Parent: "no-such-vm"}, "parent VM not found"},
		{"name equals parent", EphemeralForkOptions{Parent: parent, Name: parent}, "must differ from parent"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := SetupEphemeralFork(tc.opts)
			if err == nil {
				t.Fatalf("err = nil, want substring %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}

func TestSetupEphemeralFork_RejectsExistingChild(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	parent := "eph-parent-collide"
	stageParentVMForEphemeralFork(t, parent)
	collidingDir := filepath.Join(vmconfig.BaseDir(), "scratch-existing")
	if err := os.MkdirAll(collidingDir, 0o755); err != nil {
		t.Fatalf("create existing child dir: %v", err)
	}

	_, err := SetupEphemeralFork(EphemeralForkOptions{Parent: parent, Name: "scratch-existing"})
	if err == nil {
		t.Fatal("err = nil, want already-exists error")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("err = %q, want already-exists message", err.Error())
	}
}

func TestCleanupEphemeralFork_RefusesWithoutSentinel(t *testing.T) {
	dir := t.TempDir()
	noSentinel := filepath.Join(dir, "no-sentinel")
	if err := os.MkdirAll(noSentinel, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := CleanupEphemeralFork(noSentinel); err == nil {
		t.Fatal("CleanupEphemeralFork accepted dir without .ephemeral sentinel")
	}
	if _, err := os.Stat(noSentinel); err != nil {
		t.Errorf("dir was removed despite cleanup refusal: %v", err)
	}
}

func TestGCEphemeralForks_SkipsActiveSweepsIdle(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	base := vmconfig.BaseDir()
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatalf("mkdir base: %v", err)
	}
	mkChild := func(name string) string {
		dir := filepath.Join(base, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, ephemeralSentinel), nil, 0o644); err != nil {
			t.Fatalf("write sentinel %s: %v", name, err)
		}
		return dir
	}
	idle := mkChild("idle-eph")
	active := mkChild("active-eph")
	// A non-ephemeral dir must be ignored by the sweep.
	other := filepath.Join(base, "other-vm")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatalf("mkdir other: %v", err)
	}

	result, err := GCEphemeralForks(EphemeralGCOptions{
		IsActive: func(path string) bool { return path == active },
	})
	if err != nil {
		t.Fatalf("GCEphemeralForks: %v", err)
	}
	if result.Scanned != 2 {
		t.Errorf("Scanned = %d, want 2 (only .ephemeral dirs)", result.Scanned)
	}
	if result.SkippedAlive != 1 {
		t.Errorf("SkippedAlive = %d, want 1", result.SkippedAlive)
	}
	if result.Removed != 1 {
		t.Errorf("Removed = %d, want 1", result.Removed)
	}
	if _, err := os.Stat(idle); !os.IsNotExist(err) {
		t.Errorf("idle ephemeral still present after sweep: %v", err)
	}
	if _, err := os.Stat(active); err != nil {
		t.Errorf("active ephemeral was removed: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("non-ephemeral dir was touched: %v", err)
	}
}
