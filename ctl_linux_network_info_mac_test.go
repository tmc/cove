package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxNetworkInfoMACReadsCachedFile(t *testing.T) {
	dir := t.TempDir()
	want := "52:54:00:de:ad:be"
	if err := os.WriteFile(filepath.Join(dir, "mac.address"), []byte(want+"\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got := linuxNetworkInfoMAC(filepath.Join(dir, "control.sock"), nil)
	if got != want {
		t.Fatalf("linuxNetworkInfoMAC = %q, want %q", got, want)
	}
}
