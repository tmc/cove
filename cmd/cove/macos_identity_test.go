package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateExistingMacOSIdentityMetadataRejectsMissingWithoutRecovery(t *testing.T) {
	oldVMDir := vmDir
	oldRecover := recoverIdentity
	t.Cleanup(func() {
		vmDir = oldVMDir
		recoverIdentity = oldRecover
	})

	vmDir = t.TempDir()
	recoverIdentity = false

	err := validateExistingMacOSIdentityMetadata()
	if err == nil || !strings.Contains(err.Error(), "retry with -recover-identity") {
		t.Fatalf("validateExistingMacOSIdentityMetadata() error = %v, want recovery hint", err)
	}
}

func TestValidateExistingMacOSIdentityMetadataRecoversBadFiles(t *testing.T) {
	oldVMDir := vmDir
	oldRecover := recoverIdentity
	t.Cleanup(func() {
		vmDir = oldVMDir
		recoverIdentity = oldRecover
	})

	vmDir = t.TempDir()
	recoverIdentity = true
	for _, name := range []string{"aux.img", "hw.model", "machine.id", "mac.address"} {
		if err := os.WriteFile(filepath.Join(vmDir, name), []byte("bad"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	if err := validateExistingMacOSIdentityMetadata(); err != nil {
		t.Fatalf("validateExistingMacOSIdentityMetadata() error = %v", err)
	}
	for _, name := range []string{"aux.img", "hw.model", "machine.id"} {
		if _, err := os.Stat(filepath.Join(vmDir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s still exists after recovery, stat error = %v", name, err)
		}
	}
	matches, err := filepath.Glob(filepath.Join(vmDir, "recovery", "identity-reset-*", "mac.address"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("backed up mac.address matches = %v, want one backup", matches)
	}
}
