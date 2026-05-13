package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyStoppedForVMReportsMissingDiskSentinel(t *testing.T) {
	target := vmSelection{Directory: filepath.Join(t.TempDir(), "ghost-vm")}
	err := verifyStoppedForVM(target, false, false)
	if err == nil {
		t.Fatal("verifyStoppedForVM with missing disk: want error, got nil")
	}
	if !errors.Is(err, ErrVMDiskImageMissing) {
		t.Fatalf("err = %v, want errors.Is(err, ErrVMDiskImageMissing)", err)
	}
	if !strings.Contains(err.Error(), "cove install") {
		t.Fatalf("err = %v, want install hint", err)
	}
}

func TestVerifyStoppedForVMLinuxDiskDoesNotUseMacOSDiskHint(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "linux-disk.img"), []byte("disk"), 0o644); err != nil {
		t.Fatalf("write linux disk: %v", err)
	}
	target := vmSelection{Directory: dir, Name: "linux-vm"}
	err := verifyStoppedForVM(target, false, false)
	if err == nil {
		t.Fatal("verifyStoppedForVM linux stopped VM: want error")
	}
	if errors.Is(err, ErrVMDiskImageMissing) {
		t.Fatalf("err = %v, should not be ErrVMDiskImageMissing for linux-disk.img", err)
	}
	for _, want := range []string{"stopped Linux verification is not implemented", "cove -vm linux-vm verify"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %q, want substring %q", err.Error(), want)
		}
	}
	if strings.Contains(err.Error(), "disk.img") && !strings.Contains(err.Error(), "linux-disk.img") {
		t.Fatalf("err = %q, should not suggest macOS disk.img", err.Error())
	}
	if strings.Contains(err.Error(), "cove install") {
		t.Fatalf("err = %q, should not suggest install for existing linux disk", err.Error())
	}
}

func TestHandleVerifyRejectsPathLikeVMName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	err := handleVerify([]string{"-vm", "/tmp/not-a-vm"})
	if err == nil {
		t.Fatal("handleVerify path-like -vm succeeded; want error")
	}
	if !strings.Contains(err.Error(), "invalid VM name") {
		t.Fatalf("err = %q, want invalid VM name", err.Error())
	}
}
