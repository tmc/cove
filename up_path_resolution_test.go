package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

// TestUpPathResolutionFreshMachine simulates the cove up resolver flow on a
// fresh machine with no prior VMs. It mirrors the order of operations in
// main.go (EnsureDir with empty name) followed by handleUp (EnsureDir with
// the user-supplied -vm name) and verifies that the install path and the
// post-install resolved path agree, and that the directory the installer
// writes to actually exists.
//
// Regression test for blockers-next.md #1: install reports 100% then
// stopVMAndInject warns "disk not found" because vmDir resolved to a
// different path than the installer used.
func TestUpPathResolutionFreshMachine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Snapshot + restore globals.
	saveVMName := vmName
	saveVMDir := vmDir
	t.Cleanup(func() { vmName = saveVMName; vmDir = saveVMDir })
	vmName = ""
	vmDir = ""

	// Step 1: main.go:289 — EnsureDir(vmName="", vmDir="") on a fresh tree.
	// Resolves to ~/.vz/vms/<active> where active defaults to "default".
	mainDir, err := vmconfig.EnsureDir(vmName, vmDir)
	if err != nil {
		t.Fatalf("main EnsureDir: %v", err)
	}
	wantMain := canonPath(t, filepath.Join(home, ".vz", "vms", "default"))
	if canonPath(t, mainDir) != wantMain {
		t.Fatalf("main EnsureDir = %q, want %q", mainDir, wantMain)
	}
	vmDir = mainDir

	// Step 2: handleUp -> parseUpFlags — EnsureDir(cfg.vmName="smoketest-vm",
	// vmDir=<from step 1>). Should resolve to ~/.vz/vms/smoketest-vm and
	// MkdirAll it.
	upDir, err := vmconfig.EnsureDir("smoketest-vm", vmDir)
	if err != nil {
		t.Fatalf("up EnsureDir: %v", err)
	}
	wantUp := canonPath(t, filepath.Join(home, ".vz", "vms", "smoketest-vm"))
	if canonPath(t, upDir) != wantUp {
		t.Fatalf("up EnsureDir = %q, want %q", upDir, wantUp)
	}
	if info, err := os.Stat(upDir); err != nil {
		t.Fatalf("up EnsureDir did not create dir: %v", err)
	} else if !info.IsDir() {
		t.Fatalf("up EnsureDir target is not a directory: mode=%v", info.Mode())
	}

	// Step 3: applyUpConfig sets globals. Simulate what applyUpConfig does.
	vmName = "smoketest-vm"
	vmDir = upDir

	// Step 4: installer.go would now MkdirAll vmDir and write disk.img there.
	// We don't run the actual installer; instead we verify the path the
	// installer would use matches the path stopVMAndInject reads.
	installerVMDir := vmDir
	installerDisk := filepath.Join(installerVMDir, "disk.img")

	// Step 5: stopVMAndInject reads currentVMSelection().Directory which is
	// the global vmDir.
	target := currentVMSelection()
	stopDisk := target.diskPath()

	if installerDisk != stopDisk {
		t.Fatalf("install/stop path divergence:\n  installer disk = %q\n  stopVMAndInject disk = %q", installerDisk, stopDisk)
	}
	if target.Directory != installerVMDir {
		t.Fatalf("currentVMSelection().Directory = %q, want %q", target.Directory, installerVMDir)
	}
}

// TestUpPathResolutionWithLegacyVM verifies that when the user runs
// `cove up -vm <name>` with a legacy ~/.vz/<name> directory present
// (no alias under ~/.vz/vms/<name>), the up resolver reuses the
// legacy directory rather than creating a duplicate under ~/.vz/vms/.
// EnsureDir plants an alias under BaseDir on first sight.
func TestUpPathResolutionWithLegacyVM(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Set up a legacy VM at ~/.vz/legacy-vm/ with a disk.img stub.
	legacyDir := filepath.Join(home, ".vz", "legacy-vm")
	if err := os.MkdirAll(legacyDir, 0755); err != nil {
		t.Fatalf("MkdirAll(legacyDir): %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "disk.img"), []byte("stub"), 0644); err != nil {
		t.Fatalf("WriteFile(disk.img): %v", err)
	}

	saveVMName := vmName
	saveVMDir := vmDir
	t.Cleanup(func() { vmName = saveVMName; vmDir = saveVMDir })
	vmName = ""
	vmDir = ""

	// Step 1: main EnsureDir(empty, empty) creates ~/.vz/vms/default.
	mainDir, err := vmconfig.EnsureDir(vmName, vmDir)
	if err != nil {
		t.Fatalf("main EnsureDir: %v", err)
	}
	vmDir = mainDir

	// Step 2: up EnsureDir("legacy-vm", ...). PathCandidates checks
	// ~/.vz/vms/legacy-vm (doesn't exist) then ~/.vz/legacy-vm (exists),
	// returns the legacy path.
	upDir, err := vmconfig.EnsureDir("legacy-vm", vmDir)
	if err != nil {
		t.Fatalf("up EnsureDir: %v", err)
	}
	if got, want := upDir, legacyDir; got != want {
		// Use EvalSymlinks to canonicalize on darwin where /var -> /private/var.
		gotReal, _ := filepath.EvalSymlinks(got)
		wantReal, _ := filepath.EvalSymlinks(want)
		if gotReal != wantReal {
			t.Fatalf("up EnsureDir = %q (real %q), want %q (real %q)", got, gotReal, want, wantReal)
		}
	}

	// Verify the alias was planted under BaseDir.
	aliasPath := filepath.Join(home, ".vz", "vms", "legacy-vm")
	info, err := os.Lstat(aliasPath)
	if err != nil {
		t.Fatalf("alias not planted: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("alias %q is not a symlink (mode=%v)", aliasPath, info.Mode())
	}

	// Step 3: install/stop divergence check using the same upDir.
	vmName = "legacy-vm"
	vmDir = upDir
	target := currentVMSelection()
	if target.Directory != upDir {
		t.Fatalf("currentVMSelection().Directory = %q, want %q", target.Directory, upDir)
	}
	if got, want := target.diskPath(), filepath.Join(upDir, "disk.img"); got != want {
		t.Fatalf("target.diskPath() = %q, want %q", got, want)
	}
}

// canonPath canonicalizes a path through EvalSymlinks so darwin's
// /var → /private/var indirection doesn't break string equality.
func canonPath(t *testing.T, p string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return p
	}
	return resolved
}
