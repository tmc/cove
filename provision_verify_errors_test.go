package main

import (
	"errors"
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
