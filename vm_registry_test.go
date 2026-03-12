package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestGetVMInfoState(t *testing.T) {
	t.Run("stopped", func(t *testing.T) {
		vmPath := makeTestVMDir(t)
		info, err := GetVMInfo(vmPath)
		if err != nil {
			t.Fatalf("GetVMInfo() error = %v", err)
		}
		if info.State != "stopped" {
			t.Fatalf("GetVMInfo().State = %q, want %q", info.State, "stopped")
		}
	})

	t.Run("suspended", func(t *testing.T) {
		vmPath := makeTestVMDir(t)
		if err := os.WriteFile(filepath.Join(vmPath, "suspend.vmstate"), []byte("state"), 0644); err != nil {
			t.Fatalf("WriteFile(suspend.vmstate) error = %v", err)
		}
		info, err := GetVMInfo(vmPath)
		if err != nil {
			t.Fatalf("GetVMInfo() error = %v", err)
		}
		if info.State != "suspended" {
			t.Fatalf("GetVMInfo().State = %q, want %q", info.State, "suspended")
		}
	})

	t.Run("running", func(t *testing.T) {
		vmPath := makeTestVMDir(t)
		sock := GetControlSocketPathForVM(vmPath)
		ln, err := net.Listen("unix", sock)
		if err != nil {
			t.Fatalf("Listen(%s) error = %v", sock, err)
		}
		defer ln.Close()
		info, err := GetVMInfo(vmPath)
		if err != nil {
			t.Fatalf("GetVMInfo() error = %v", err)
		}
		if info.State != "running" {
			t.Fatalf("GetVMInfo().State = %q, want %q", info.State, "running")
		}
	})
}

func makeTestVMDir(t *testing.T) string {
	t.Helper()

	vmPath := t.TempDir()
	for _, name := range []string{"disk.img", "aux.img"} {
		if err := os.WriteFile(filepath.Join(vmPath, name), []byte(name), 0644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}
	return vmPath
}
