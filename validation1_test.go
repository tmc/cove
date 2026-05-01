package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/apple/foundation"
	pvz "github.com/tmc/apple/private/virtualization"
	"golang.org/x/sys/unix"
)

// TestValidation1_MultiAttachRO answers the empirical question raised by
// design 013 Phase 3 Model B: does VZ allow N concurrent read-only
// attachments to the same disk.img via VZTemporaryRAMStorageDeviceAttachment?
//
// Approach:
//
//  1. Pick a populated disk.img on the host (a stopped VM's primary disk).
//  2. Create attachment A against it with readOnly=true.
//  3. Create attachment B against the SAME path with readOnly=true.
//  4. Probe with a host-side flock(LOCK_EX|LOCK_NB) to see whether VZ
//     opened the file with no lock, LOCK_SH, or LOCK_EX.
//
// Pass/fail decides Model B vs clonefile-per-child fallback for Phase 3.
// Skipped on non-darwin or when no parent disk is available — this is a
// host-only empirical probe, not a CI test.
func TestValidation1_MultiAttachRO(t *testing.T) {
	parentDir := os.Getenv("COVE_VALIDATION1_PARENT_DIR")
	if parentDir == "" {
		// Default to the Phase 2 smoke parent if it still exists.
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skipf("user home dir: %v", err)
		}
		candidate := filepath.Join(home, ".vz", "vms", "overlay-fresh-20260429-165003")
		if _, err := os.Stat(candidate); err != nil {
			t.Skipf("set COVE_VALIDATION1_PARENT_DIR or stage a parent VM at %s", candidate)
		}
		parentDir = candidate
	}
	diskPath := filepath.Join(parentDir, "disk.img")
	if _, err := os.Stat(diskPath); err != nil {
		t.Fatalf("parent disk %q: %v", diskPath, err)
	}

	make := func(label string) (pvz.VZStorageDeviceAttachment, error) {
		url := foundation.NewURLFileURLWithPath(diskPath)
		if url.ID == 0 {
			t.Fatalf("%s: NewURLFileURLWithPath returned nil", label)
		}
		url.Retain()
		return createRuntimeStorageDeviceAttachment(diskPath, true, systemDiskAttachmentTemporaryRAM)
	}

	a, err := make("A")
	if err != nil {
		t.Fatalf("attachment A failed: %v", err)
	}
	t.Logf("attachment A: id=%v", a.ID)

	b, err := make("B")
	if err != nil {
		// B was rejected. Record the framework's verdict and pick the
		// fallback path for Phase 3.
		t.Logf("RESULT: VZ REJECTED concurrent read-only attachments. err=%v", err)
		t.Logf("Phase 3 must fall back to clonefile-per-child (Model A in design 013).")
		return
	}
	t.Logf("attachment B: id=%v", b.ID)

	// Both attachments succeeded. Probe the actual file-lock posture from
	// a third descriptor: if VZ took LOCK_EX, our probe will fail; if
	// LOCK_SH, our LOCK_EX probe fails but a LOCK_SH probe succeeds; if
	// no lock, both succeed.
	f, err := os.OpenFile(diskPath, os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("open disk for probe: %v", err)
	}
	defer f.Close()

	exErr := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if exErr == nil {
		t.Logf("probe: LOCK_EX|LOCK_NB succeeded — VZ holds NO file lock on the disk.")
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
	} else {
		t.Logf("probe: LOCK_EX|LOCK_NB rejected (%v) — VZ holds at least LOCK_SH.", exErr)
		shErr := unix.Flock(int(f.Fd()), unix.LOCK_SH|unix.LOCK_NB)
		if shErr == nil {
			t.Logf("probe: LOCK_SH|LOCK_NB succeeded — VZ uses LOCK_SH (compatible with N readers).")
			_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		} else {
			t.Logf("probe: LOCK_SH|LOCK_NB also rejected (%v) — unexpected lock posture.", shErr)
		}
	}

	t.Logf("RESULT: VZ ALLOWED concurrent read-only attachments. Model B is feasible.")
}
