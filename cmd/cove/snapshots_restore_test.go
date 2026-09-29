package main

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiskSnapshotRestoreRefusesRunningVM(t *testing.T) {
	dir := t.TempDir()
	mgr := NewDiskSnapshotManager(dir)

	// Set up snapshot and live disk
	liveDisk := filepath.Join(dir, "disk.img")
	if err := os.WriteFile(liveDisk, []byte("live disk"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Save("snap1", DiskSnapshotSystem, ""); err != nil {
		t.Fatal(err)
	}

	// Mock running VM by listening on control socket
	sock := GetControlSocketPathForVM(dir)
	if err := os.MkdirAll(filepath.Dir(sock), 0755); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	// 1. handleDiskSnapshotRestore should refuse
	err = handleDiskSnapshotRestore(mgr, []string{"-y", "snap1"})
	if err == nil || !strings.Contains(err.Error(), "must be stopped") {
		t.Fatalf("handleDiskSnapshotRestore err = %v, want 'must be stopped'", err)
	}

	// 2. mgr.Restore should refuse
	err = mgr.Restore("snap1", DiskSnapshotSystem)
	if err == nil || !strings.Contains(err.Error(), "must be stopped") {
		t.Fatalf("mgr.Restore err = %v, want 'must be stopped'", err)
	}
}

func TestDiskSnapshotRestoreConfirmation(t *testing.T) {
	oldIsTerminal := confirmStdinIsTerminal
	oldStdin := confirmStdin
	oldStderr := confirmStderr
	oldExit := confirmExit
	t.Cleanup(func() {
		confirmStdinIsTerminal = oldIsTerminal
		confirmStdin = oldStdin
		confirmStderr = oldStderr
		confirmExit = oldExit
	})

	dir := t.TempDir()
	mgr := NewDiskSnapshotManager(dir)
	liveDisk := filepath.Join(dir, "disk.img")
	if err := os.WriteFile(liveDisk, []byte("original live"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Save("snap1", DiskSnapshotSystem, ""); err != nil {
		t.Fatal(err)
	}
	// Write new content to live disk
	if err := os.WriteFile(liveDisk, []byte("modified live"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Run("non-interactive without -y exits 2", func(t *testing.T) {
		confirmStdinIsTerminal = func() bool { return false }
		var stderr bytes.Buffer
		confirmStderr = &stderr
		var exitCode int
		confirmExit = func(code int) {
			exitCode = code
			panic("exit called")
		}

		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("expected panic from exit")
			}
			if exitCode != 2 {
				t.Errorf("exitCode = %d, want 2", exitCode)
			}
			if !strings.Contains(stderr.String(), "deletion requires confirmation; use -y/--yes") {
				t.Errorf("stderr = %q, want confirmation prompt warning", stderr.String())
			}
		}()

		_ = handleDiskSnapshotRestore(mgr, []string{"snap1"})
	})

	t.Run("interactive decline exits 1", func(t *testing.T) {
		confirmStdinIsTerminal = func() bool { return true }
		confirmStdin = strings.NewReader("n\n")
		var stderr bytes.Buffer
		confirmStderr = &stderr
		var exitCode int
		confirmExit = func(code int) {
			exitCode = code
			panic("exit called")
		}

		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("expected panic from exit")
			}
			if exitCode != 1 {
				t.Errorf("exitCode = %d, want 1", exitCode)
			}
			if !strings.Contains(stderr.String(), "aborted") {
				t.Errorf("stderr = %q, want 'aborted'", stderr.String())
			}
		}()

		_ = handleDiskSnapshotRestore(mgr, []string{"snap1"})
	})

	t.Run("interactive accept restores snapshot", func(t *testing.T) {
		confirmStdinIsTerminal = func() bool { return true }
		confirmStdin = strings.NewReader("y\n")
		var stderr bytes.Buffer
		confirmStderr = &stderr
		confirmExit = func(code int) {
			t.Fatalf("unexpected exit: %d", code)
		}

		err := handleDiskSnapshotRestore(mgr, []string{"snap1"})
		if err != nil {
			t.Fatalf("handleDiskSnapshotRestore: %v", err)
		}
		data, err := os.ReadFile(liveDisk)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "original live" {
			t.Errorf("live disk content = %q, want 'original live'", string(data))
		}
	})
}

func TestDiskSnapshotRestoreFlags(t *testing.T) {
	for _, flag := range []string{"-y", "--yes", "-yes"} {
		t.Run("yes flag "+flag, func(t *testing.T) {
			dir := t.TempDir()
			mgr := NewDiskSnapshotManager(dir)
			liveDisk := filepath.Join(dir, "disk.img")
			if err := os.WriteFile(liveDisk, []byte("snap content"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := mgr.Save("snap1", DiskSnapshotSystem, ""); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(liveDisk, []byte("changed content"), 0644); err != nil {
				t.Fatal(err)
			}

			if err := handleDiskSnapshotRestore(mgr, []string{flag, "snap1"}); err != nil {
				t.Fatalf("handleDiskSnapshotRestore with %s: %v", flag, err)
			}
			data, err := os.ReadFile(liveDisk)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != "snap content" {
				t.Errorf("disk content = %q, want 'snap content'", string(data))
			}
		})
	}

	t.Run("unknown flags rejected", func(t *testing.T) {
		dir := t.TempDir()
		mgr := NewDiskSnapshotManager(dir)

		tests := []struct {
			name string
			args []string
			want string
		}{
			{"unknown option -vm", []string{"snap1", "-vm", "other"}, "unknown disk-snapshot restore option: -vm"},
			{"unknown option -foo", []string{"snap1", "-foo"}, "unknown disk-snapshot restore option: -foo"},
			{"typo --yess", []string{"--yess", "snap1"}, "unknown disk-snapshot restore option: --yess"},
			{"extra positional arg", []string{"snap1", "snap2"}, "unknown disk-snapshot restore option: snap2"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				err := handleDiskSnapshotRestore(mgr, tt.args)
				if err == nil || !strings.Contains(err.Error(), tt.want) {
					t.Fatalf("err = %v, want substring %q", err, tt.want)
				}
			})
		}
	})
}

func TestDiskSnapshotRestoreAtomicFailure(t *testing.T) {
	dir := t.TempDir()
	mgr := NewDiskSnapshotManager(dir)

	liveDisk := filepath.Join(dir, "disk.img")
	originalLiveContent := []byte("original live disk state")
	if err := os.WriteFile(liveDisk, originalLiveContent, 0644); err != nil {
		t.Fatal(err)
	}

	if err := mgr.Save("snap1", DiskSnapshotSystem, ""); err != nil {
		t.Fatal(err)
	}

	snapDisk := filepath.Join(dir, "disk-snapshots", "snap1", "disk.img")
	// Make snapshot disk unreadable so cloning will fail.
	if err := os.Chmod(snapDisk, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		os.Chmod(snapDisk, 0644)
	})

	err := mgr.Restore("snap1", DiskSnapshotSystem)
	if err == nil {
		t.Fatal("expected restore to fail due to unreadable snapshot disk")
	}

	// Verify original live disk is preserved intact.
	gotContent, err := os.ReadFile(liveDisk)
	if err != nil {
		t.Fatalf("original live disk missing: %v", err)
	}
	if !bytes.Equal(gotContent, originalLiveContent) {
		t.Fatalf("live disk content modified: got %q, want %q", gotContent, originalLiveContent)
	}

	// Verify no temporary restore files remain in vmDir.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "disk.img.restore.") {
			t.Errorf("found leftover restore temp file: %s", entry.Name())
		}
	}
}
