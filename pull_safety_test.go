package main

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnsurePullTargetInactiveNoSocket(t *testing.T) {
	vmPath := shortPullSafetyVMDir(t)
	if err := os.MkdirAll(vmPath, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := ensurePullTargetInactiveWithTimeout(vmPath, 50*time.Millisecond); err != nil {
		t.Fatalf("ensurePullTargetInactiveWithTimeout(): %v", err)
	}
}

func TestEnsurePullTargetInactiveRemovesStaleSocket(t *testing.T) {
	vmPath := shortPullSafetyVMDir(t)
	if err := os.MkdirAll(vmPath, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	sock := GetControlSocketPathForVM(vmPath)
	if err := os.WriteFile(sock, []byte("stale"), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := ensurePullTargetInactiveWithTimeout(vmPath, 50*time.Millisecond); err != nil {
		t.Fatalf("ensurePullTargetInactiveWithTimeout(): %v", err)
	}
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Fatalf("stale socket still exists, stat error = %v", err)
	}
}

func TestEnsurePullTargetInactiveActiveSocket(t *testing.T) {
	vmPath := shortPullSafetyVMDir(t)
	if err := os.MkdirAll(vmPath, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	ln := listenPullSafetySocket(t, vmPath, func(conn net.Conn) {
		defer conn.Close()
		if _, err := bufio.NewReader(conn).ReadBytes('\n'); err != nil {
			return
		}
		_, _ = conn.Write([]byte(`{"success":true}` + "\n"))
	})
	defer ln.Close()

	err := ensurePullTargetInactiveWithTimeout(vmPath, time.Second)
	if err == nil || !strings.Contains(err.Error(), `cannot pull into an active VM "macos-3"`) {
		t.Fatalf("ensurePullTargetInactiveWithTimeout() error = %v, want active VM", err)
	}
}

func TestEnsurePullTargetInactiveAmbiguousSocket(t *testing.T) {
	vmPath := shortPullSafetyVMDir(t)
	if err := os.MkdirAll(vmPath, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	sock := GetControlSocketPathForVM(vmPath)
	ln := listenPullSafetySocket(t, vmPath, func(conn net.Conn) {
		defer conn.Close()
		time.Sleep(200 * time.Millisecond)
	})
	defer ln.Close()

	err := ensurePullTargetInactiveWithTimeout(vmPath, 50*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "probe control socket") {
		t.Fatalf("ensurePullTargetInactiveWithTimeout() error = %v, want probe error", err)
	}
	if _, statErr := os.Stat(sock); statErr != nil {
		t.Fatalf("active socket was removed, stat error = %v", statErr)
	}
}

func TestCheckIncompletePullDisk(t *testing.T) {
	vmPath := shortPullSafetyVMDir(t)
	if err := os.MkdirAll(vmPath, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	diskPath := filepath.Join(vmPath, "disk.img")
	partial := pullPartialDiskPath(diskPath)
	if err := os.WriteFile(partial, []byte("partial"), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	err := checkIncompletePullDisk(vmPath, diskPath)
	if err == nil || !strings.Contains(err.Error(), "pull was interrupted") || !strings.Contains(err.Error(), partial) {
		t.Fatalf("checkIncompletePullDisk() error = %v, want interrupted pull path", err)
	}
}

func shortPullSafetyVMDir(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp(os.TempDir(), "vzpull-")
	if err != nil {
		t.Fatalf("MkdirTemp() error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "macos-3")
}

func listenPullSafetySocket(t *testing.T, vmPath string, serve func(net.Conn)) net.Listener {
	t.Helper()

	ln, err := net.Listen("unix", GetControlSocketPathForVM(vmPath))
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		serve(conn)
	}()
	return ln
}
