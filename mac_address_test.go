package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadOrCreateMACAddressForVMLoadsExisting(t *testing.T) {
	vmDir := t.TempDir()
	want := "aa:bb:cc:dd:ee:ff"
	if err := os.WriteFile(filepath.Join(vmDir, "mac.address"), []byte(want+"\n"), 0644); err != nil {
		t.Fatalf("write mac.address: %v", err)
	}
	got := loadOrCreateMACAddressForVM(vmDir)
	if got.ID == 0 {
		t.Fatal("loadOrCreateMACAddressForVM() returned zero MAC")
	}
	if got.String() != want {
		t.Fatalf("loadOrCreateMACAddressForVM() = %q, want %q", got.String(), want)
	}
}

func TestLoadOrCreateMACAddressForVMPersistsGenerated(t *testing.T) {
	vmDir := t.TempDir()
	first := loadOrCreateMACAddressForVM(vmDir)
	if first.ID == 0 {
		t.Fatal("first MAC is zero")
	}
	data, err := os.ReadFile(filepath.Join(vmDir, "mac.address"))
	if err != nil {
		t.Fatalf("read mac.address: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != first.String() {
		t.Fatalf("mac.address = %q, want %q", got, first.String())
	}
	second := loadOrCreateMACAddressForVM(vmDir)
	if second.String() != first.String() {
		t.Fatalf("second MAC = %q, want stable %q", second.String(), first.String())
	}
}
