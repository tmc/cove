package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallDaemonPlistMkdirPlistError(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	paths := daemonPaths{
		SocketPath: filepath.Join(dir, ".vz", "cove.sock"),
		PIDPath:    filepath.Join(dir, ".vz", "cove.pid"),
		PlistPath:  filepath.Join(blocker, "LaunchAgents", "com.cove.daemon.plist"),
		LogPath:    filepath.Join(dir, ".vz", "coved.log"),
		CovedPath:  filepath.Join(dir, "bin", "coved"),
	}
	err := installDaemonPlist(paths)
	if err == nil || !strings.Contains(err.Error(), "create launch agents dir") {
		t.Fatalf("err = %v, want create launch agents dir", err)
	}
}

func TestInstallDaemonPlistMkdirSocketError(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	paths := daemonPaths{
		SocketPath: filepath.Join(blocker, ".vz", "cove.sock"),
		PIDPath:    filepath.Join(dir, ".vz", "cove.pid"),
		PlistPath:  filepath.Join(dir, "LaunchAgents", "com.cove.daemon.plist"),
		LogPath:    filepath.Join(dir, ".vz", "coved.log"),
		CovedPath:  filepath.Join(dir, "bin", "coved"),
	}
	err := installDaemonPlist(paths)
	if err == nil || !strings.Contains(err.Error(), "create daemon state dir") {
		t.Fatalf("err = %v, want create daemon state dir", err)
	}
}
