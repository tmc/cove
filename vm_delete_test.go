package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

// TestDeleteVM_RefusesParentWithChildren asserts the lineage policy:
// deleting a parent without --cascade fails when at least one VM
// records ParentVM == parent. The error names the dependents so the
// operator can decide whether to cascade.
func TestDeleteVM_RefusesParentWithChildren(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeTreeVM(t, "parent-a", vmconfig.Config{})
	writeTreeVM(t, "child-a", vmconfig.Config{ParentVM: "parent-a"})
	writeTreeVM(t, "child-b", vmconfig.Config{ParentVM: "parent-a"})

	err := DeleteVMWithOptions("parent-a", DeleteVMOptions{})
	if err == nil {
		t.Fatal("DeleteVMWithOptions(parent-a, {}) returned nil; want refusal")
	}
	if !strings.Contains(err.Error(), "fork descendant") {
		t.Errorf("err = %q, want substring 'fork descendant'", err.Error())
	}
	if !strings.Contains(err.Error(), "child-a") || !strings.Contains(err.Error(), "child-b") {
		t.Errorf("err = %q, want both child names listed", err.Error())
	}
	if !strings.Contains(err.Error(), "--cascade") {
		t.Errorf("err = %q, want --cascade hint", err.Error())
	}
	// Parent must NOT have been deleted on a refusal.
	if _, statErr := os.Stat(filepath.Join(vmconfig.BaseDir(), "parent-a")); statErr != nil {
		t.Errorf("parent-a directory removed despite refusal: %v", statErr)
	}
}

// TestDeleteVM_CascadeRemovesChildrenFirst asserts --cascade
// recursively deletes descendants before the parent. Multi-level:
// parent → child → grandchild all removed.
func TestDeleteVM_CascadeRemovesChildrenFirst(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeTreeVM(t, "ancestor", vmconfig.Config{})
	writeTreeVM(t, "kid", vmconfig.Config{ParentVM: "ancestor"})
	writeTreeVM(t, "grandkid", vmconfig.Config{ParentVM: "kid"})

	if err := DeleteVMWithOptions("ancestor", DeleteVMOptions{Cascade: true}); err != nil {
		t.Fatalf("DeleteVMWithOptions(ancestor, Cascade) error = %v", err)
	}
	for _, name := range []string{"ancestor", "kid", "grandkid"} {
		if _, err := os.Stat(filepath.Join(vmconfig.BaseDir(), name)); !os.IsNotExist(err) {
			t.Errorf("VM %q still exists after cascade: err=%v", name, err)
		}
	}
}

// TestDeleteVM_ParentlessDeletesClean pins the no-children branch:
// a VM with no descendants deletes successfully under the
// (default) lineage check.
func TestDeleteVM_ParentlessDeletesClean(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeTreeVM(t, "solo", vmconfig.Config{})

	if err := DeleteVMWithOptions("solo", DeleteVMOptions{}); err != nil {
		t.Fatalf("DeleteVMWithOptions(solo, {}) error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(vmconfig.BaseDir(), "solo")); !os.IsNotExist(err) {
		t.Errorf("solo directory still exists after delete: %v", err)
	}
}

// TestChildVMNames_SortsAndExcludesSelf asserts the helper used by
// the CLI prompt: children sorted lexicographically, target VM
// itself excluded even if it (somehow) has ParentVM set.
func TestChildVMNames_SortsAndExcludesSelf(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeTreeVM(t, "root", vmconfig.Config{})
	writeTreeVM(t, "zeta", vmconfig.Config{ParentVM: "root"})
	writeTreeVM(t, "alpha", vmconfig.Config{ParentVM: "root"})
	writeTreeVM(t, "mid", vmconfig.Config{ParentVM: "root"})

	got, err := childVMNames("root")
	if err != nil {
		t.Fatalf("childVMNames error = %v", err)
	}
	want := []string{"alpha", "mid", "zeta"}
	if len(got) != len(want) {
		t.Fatalf("childVMNames len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("childVMNames[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestDeleteVMWithOptionsNotFound(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	err := DeleteVMWithOptions("ghost-vm-r302", DeleteVMOptions{})
	if err == nil {
		t.Fatal("DeleteVMWithOptions(ghost) = nil, want not-found error")
	}
	if !strings.Contains(err.Error(), "vm not found") {
		t.Fatalf("err = %v, want 'vm not found'", err)
	}
}

func TestDeleteVMWithOptionsRejectsNonDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	base := vmconfig.BaseDir()
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "file-vm"), nil, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	err := DeleteVMWithOptions("file-vm", DeleteVMOptions{})
	if err == nil {
		t.Fatal("DeleteVMWithOptions(file) = nil, want non-dir error")
	}
	if !strings.Contains(err.Error(), "not a VM directory") {
		t.Fatalf("err = %v, want 'not a VM directory'", err)
	}
}
