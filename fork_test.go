package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/vmconfig"
)

func sha256File(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatalf("hash %s: %v", path, err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TestForkVMDisk_CoWDivergence proves the fork primitive: a child cloned
// from a parent starts byte-identical, but writes to the child diverge from
// the parent without touching it. This is the core invariant that lets
// "disk-snapshot restore" preserve the snapshot while the live VM mutates
// its own disk (Model A in docs/designs/013-vm-fork.md).
func TestForkVMDisk_CoWDivergence(t *testing.T) {
	dir := t.TempDir()
	if !SupportsClonefile(dir) {
		t.Skip("filesystem does not support clonefile")
	}

	parent := filepath.Join(dir, "parent.img")
	child := filepath.Join(dir, "child.img")

	original := make([]byte, 64*1024)
	for i := range original {
		original[i] = byte(i % 251)
	}
	if err := os.WriteFile(parent, original, 0644); err != nil {
		t.Fatalf("write parent: %v", err)
	}

	parentHashBefore := sha256File(t, parent)

	if err := ForkVMDisk(parent, child); err != nil {
		t.Fatalf("ForkVMDisk: %v", err)
	}

	childHashAfterFork := sha256File(t, child)
	if childHashAfterFork != parentHashBefore {
		t.Fatalf("child differs from parent immediately after fork: parent=%s child=%s",
			parentHashBefore, childHashAfterFork)
	}

	f, err := os.OpenFile(child, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open child for write: %v", err)
	}
	if _, err := f.WriteAt([]byte{0xFF}, 17); err != nil {
		f.Close()
		t.Fatalf("write child: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close child: %v", err)
	}

	parentHashAfter := sha256File(t, parent)
	childHashAfter := sha256File(t, child)

	if parentHashAfter != parentHashBefore {
		t.Fatalf("parent mutated by child write: before=%s after=%s",
			parentHashBefore, parentHashAfter)
	}
	if childHashAfter == childHashAfterFork {
		t.Fatalf("child hash unchanged after write — divergence failed: %s", childHashAfter)
	}
}

func TestForkVMDisk_RejectsExistingChild(t *testing.T) {
	dir := t.TempDir()
	if !SupportsClonefile(dir) {
		t.Skip("filesystem does not support clonefile")
	}

	parent := filepath.Join(dir, "parent.img")
	child := filepath.Join(dir, "child.img")

	if err := os.WriteFile(parent, []byte("hi"), 0644); err != nil {
		t.Fatalf("write parent: %v", err)
	}
	if err := os.WriteFile(child, []byte("existing"), 0644); err != nil {
		t.Fatalf("write child: %v", err)
	}

	err := ForkVMDisk(parent, child)
	if err == nil {
		t.Fatal("expected error when child already exists")
	}
}

func TestForkVMDisk_RejectsMissingParent(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Join(dir, "missing.img")
	child := filepath.Join(dir, "child.img")

	err := ForkVMDisk(parent, child)
	if err == nil {
		t.Fatal("expected error when parent is missing")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected ErrNotExist, got: %v", err)
	}
}

func TestForkVMDisk_RejectsEmptyPaths(t *testing.T) {
	if err := ForkVMDisk("", "child"); err == nil {
		t.Error("expected error for empty parent")
	}
	if err := ForkVMDisk("parent", ""); err == nil {
		t.Error("expected error for empty child")
	}
}

// TestForkVM_CreatesChildWithUniqueIdentity proves the high-level fork
// command's contract: the child gets its own machine.id (fresh identity)
// while disk/aux/hw.model start identical to the parent. Only the
// machine.id differs at fork time.
func TestForkVM_CreatesChildWithUniqueIdentity(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	parent := "parent-vm"
	parentDir := filepath.Join(vmconfig.BaseDir(), parent)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	files := map[string][]byte{
		"disk.img":   bytes64KCounting(),
		"aux.img":    []byte("parent-aux-bytes"),
		"hw.model":   []byte("parent-hw-model"),
		"machine.id": []byte("PARENT-MACHINE-ID-VALUE-32BYTES!"),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(parentDir, name), data, 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	if err := ForkVM(parent, "child-vm"); err != nil {
		t.Fatalf("ForkVM: %v", err)
	}

	childDir := filepath.Join(vmconfig.BaseDir(), "child-vm")

	for _, name := range []string{"disk.img", "aux.img", "hw.model"} {
		got, err := os.ReadFile(filepath.Join(childDir, name))
		if err != nil {
			t.Fatalf("read child %s: %v", name, err)
		}
		want := files[name]
		if string(got) != string(want) {
			t.Errorf("child %s differs from parent: got %d bytes, want %d", name, len(got), len(want))
		}
	}

	childID, err := os.ReadFile(filepath.Join(childDir, "machine.id"))
	if err != nil {
		t.Fatalf("read child machine.id: %v", err)
	}
	if string(childID) == string(files["machine.id"]) {
		t.Errorf("child machine.id matches parent — must be regenerated; got %q", childID)
	}
	if len(childID) == 0 {
		t.Errorf("child machine.id is empty")
	}

	macPath := filepath.Join(childDir, "mac.address")
	if _, err := os.Stat(macPath); err == nil {
		t.Errorf("child mac.address must not exist after fork; first boot allocates it")
	}

	suspendPath := filepath.Join(childDir, "suspend.vmstate")
	if _, err := os.Stat(suspendPath); err == nil {
		t.Errorf("child suspend.vmstate must not exist after fork (cold boot is intended)")
	}

	cfg, err := vmconfig.Load(childDir)
	if err != nil {
		t.Fatalf("load child config: %v", err)
	}
	if cfg.ParentVM != parent {
		t.Errorf("child ParentVM = %q, want %q", cfg.ParentVM, parent)
	}
	if cfg.ParentSnapshot != "" {
		t.Errorf("child ParentSnapshot = %q, want empty", cfg.ParentSnapshot)
	}
	if cfg.ForkedAt.IsZero() {
		t.Errorf("child ForkedAt is zero")
	}
}

func TestForkVM_RejectsBadArgs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := ForkVM("", "child"); err == nil {
		t.Error("expected error for empty parent")
	}
	if err := ForkVM("parent", ""); err == nil {
		t.Error("expected error for empty child")
	}
	if err := ForkVM("same", "same"); err == nil {
		t.Error("expected error when parent == child")
	}
	if err := ForkVM("does-not-exist", "child"); err == nil {
		t.Error("expected error when parent VM is missing")
	}
}

func bytes64KCounting() []byte {
	b := make([]byte, 64*1024)
	for i := range b {
		b[i] = byte(i % 251)
	}
	return b
}

// stageParentVMForSnapshotFork writes a parent VM directory with the
// minimal files needed for ForkVMWithSnapshot, plus a saved
// vmstate snapshot at parent/snapshots/<snapshot>.vmstate. Returns
// the parent name and the byte content of the snapshot, so tests can
// verify it is copied byte-for-byte.
func stageParentVMForSnapshotFork(t *testing.T, parent, snapshot string) (parentDir string, vmstateContents []byte) {
	t.Helper()
	parentDir = filepath.Join(vmconfig.BaseDir(), parent)
	if err := os.MkdirAll(filepath.Join(parentDir, "snapshots"), 0755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	files := map[string][]byte{
		"disk.img":   bytes64KCounting(),
		"aux.img":    []byte("parent-aux-bytes-with-snapshot-state"),
		"hw.model":   []byte("parent-hw-model"),
		"machine.id": []byte("PARENT-MACHINE-ID-VALUE-32BYTES!"),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(parentDir, name), data, 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	vmstateContents = []byte("VMSTATE-PAYLOAD-FOR-" + snapshot)
	if err := os.WriteFile(filepath.Join(parentDir, "snapshots", snapshot+".vmstate"), vmstateContents, 0644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
	return parentDir, vmstateContents
}

func TestForkVMWithSnapshot_HappyPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	parent := "snap-parent"
	parentDir, vmstateContents := stageParentVMForSnapshotFork(t, parent, "clean")

	if err := ForkVMWithSnapshot(ForkVMOptions{Parent: parent, Child: "snap-child", Snapshot: "clean"}); err != nil {
		t.Fatalf("ForkVMWithSnapshot: %v", err)
	}

	childDir := filepath.Join(vmconfig.BaseDir(), "snap-child")
	for _, name := range []string{"disk.img", "aux.img", "hw.model", "suspend.vmstate"} {
		if _, err := os.Stat(filepath.Join(childDir, name)); err != nil {
			t.Errorf("child missing %s: %v", name, err)
		}
	}

	// suspend.vmstate must be byte-identical to the parent's snapshot file.
	gotState, err := os.ReadFile(filepath.Join(childDir, "suspend.vmstate"))
	if err != nil {
		t.Fatalf("read child suspend.vmstate: %v", err)
	}
	if string(gotState) != string(vmstateContents) {
		t.Fatalf("child suspend.vmstate %q, want %q", gotState, vmstateContents)
	}

	// aux.img must be byte-identical to parent's aux.img — A1 requires it.
	gotAux, err := os.ReadFile(filepath.Join(childDir, "aux.img"))
	if err != nil {
		t.Fatalf("read child aux.img: %v", err)
	}
	wantAux, err := os.ReadFile(filepath.Join(parentDir, "aux.img"))
	if err != nil {
		t.Fatalf("read parent aux.img: %v", err)
	}
	if string(gotAux) != string(wantAux) {
		t.Fatal("child aux.img differs from parent aux.img — A1 requires byte-for-byte copy")
	}
}

func TestForkVMWithSnapshot_LineageRecordsSnapshot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	parent := "snap-parent-lineage"
	stageParentVMForSnapshotFork(t, parent, "production")

	if err := ForkVMWithSnapshot(ForkVMOptions{Parent: parent, Child: "snap-child-lineage", Snapshot: "production"}); err != nil {
		t.Fatalf("ForkVMWithSnapshot: %v", err)
	}

	cfg, err := vmconfig.Load(filepath.Join(vmconfig.BaseDir(), "snap-child-lineage"))
	if err != nil {
		t.Fatalf("load child config: %v", err)
	}
	if cfg.ParentVM != parent {
		t.Errorf("child ParentVM = %q, want %q", cfg.ParentVM, parent)
	}
	if cfg.ParentSnapshot != "production" {
		t.Errorf("child ParentSnapshot = %q, want %q", cfg.ParentSnapshot, "production")
	}
	if cfg.ForkedAt.IsZero() {
		t.Error("child ForkedAt is zero")
	}
}

func TestForkVMWithSnapshot_RejectsMissingSnapshot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	parent := "snap-parent-missing"
	stageParentVMForSnapshotFork(t, parent, "exists")

	err := ForkVMWithSnapshot(ForkVMOptions{Parent: parent, Child: "snap-child-missing", Snapshot: "does-not-exist"})
	if err == nil {
		t.Fatal("ForkVMWithSnapshot returned nil; want missing-snapshot error")
	}
	if !strings.Contains(err.Error(), "not found") || !strings.Contains(err.Error(), "does-not-exist") {
		t.Errorf("err = %q, want substring about missing snapshot", err.Error())
	}
	// Child must not have been created on a pre-check failure.
	childDir := filepath.Join(vmconfig.BaseDir(), "snap-child-missing")
	if _, statErr := os.Stat(childDir); statErr == nil {
		t.Error("child VM dir created despite missing-snapshot pre-check failure")
	}
}

func TestForkVMWithSnapshot_RejectsRunningParent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	parent := "snap-parent-running"
	parentDir, _ := stageParentVMForSnapshotFork(t, parent, "stopped")

	// Simulate a running parent by holding the run.lock ourselves.
	parentLock, err := AcquireRunLock(parentDir)
	if err != nil {
		t.Fatalf("acquire parent lock: %v", err)
	}
	defer parentLock.Release()

	err = ForkVMWithSnapshot(ForkVMOptions{Parent: parent, Child: "snap-child-running", Snapshot: "stopped"})
	if err == nil {
		t.Fatal("ForkVMWithSnapshot returned nil; want parent-running error")
	}
	if !strings.Contains(err.Error(), "is running") {
		t.Errorf("err = %q, want substring about parent running", err.Error())
	}
	childDir := filepath.Join(vmconfig.BaseDir(), "snap-child-running")
	if _, statErr := os.Stat(childDir); statErr == nil {
		t.Error("child VM dir created despite parent-running pre-check failure")
	}
}

func TestForkVMWithSnapshot_RejectsBadSnapshotName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	parent := "snap-parent-bad-name"
	stageParentVMForSnapshotFork(t, parent, "fine")

	// Empty snapshot is NOT a validation error — it delegates to ForkVM
	// (covered by TestForkVMWithSnapshot_EmptySnapshotDelegatesToForkVM).
	for _, bad := range []string{"../escape", "with/slash"} {
		err := ForkVMWithSnapshot(ForkVMOptions{Parent: parent, Child: "snap-child-bad", Snapshot: bad})
		if err == nil {
			t.Errorf("ForkVMWithSnapshot with snapshot %q returned nil; want validation error", bad)
		}
	}
}

func TestForkVMWithSnapshot_EmptySnapshotDelegatesToForkVM(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	parent := "snap-parent-delegate"
	stageParentVMForSnapshotFork(t, parent, "ignored")

	if err := ForkVMWithSnapshot(ForkVMOptions{Parent: parent, Child: "snap-child-delegate", Snapshot: ""}); err != nil {
		t.Fatalf("ForkVMWithSnapshot empty snapshot: %v", err)
	}

	// Per Phase 1 invariant: empty-snapshot fork does not seed suspend.vmstate.
	suspendPath := filepath.Join(vmconfig.BaseDir(), "snap-child-delegate", "suspend.vmstate")
	if _, err := os.Stat(suspendPath); err == nil {
		t.Error("child suspend.vmstate exists after empty-snapshot fork; want delegated to ForkVM (cold boot)")
	}

	// And ParentSnapshot must remain empty in lineage.
	cfg, err := vmconfig.Load(filepath.Join(vmconfig.BaseDir(), "snap-child-delegate"))
	if err != nil {
		t.Fatalf("load child config: %v", err)
	}
	if cfg.ParentSnapshot != "" {
		t.Errorf("ParentSnapshot = %q, want empty for delegated path", cfg.ParentSnapshot)
	}
}

func TestForkVMWithSnapshot_RejectsBadArgs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	parent := "snap-parent-bad-args"
	stageParentVMForSnapshotFork(t, parent, "snap")

	for _, tc := range []struct {
		name string
		opts ForkVMOptions
	}{
		{"empty parent", ForkVMOptions{Parent: "", Child: "child", Snapshot: "snap"}},
		{"empty child", ForkVMOptions{Parent: parent, Child: "", Snapshot: "snap"}},
		{"parent equals child", ForkVMOptions{Parent: "same", Child: "same", Snapshot: "snap"}},
		{"missing parent VM", ForkVMOptions{Parent: "does-not-exist", Child: "child", Snapshot: "snap"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ForkVMWithSnapshot(tc.opts); err == nil {
				t.Errorf("ForkVMWithSnapshot %+v returned nil; want error", tc.opts)
			}
		})
	}
}
