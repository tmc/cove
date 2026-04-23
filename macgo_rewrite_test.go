//go:build darwin

package main

import (
	"bytes"
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestHelperCommandDoesNotRewriteBinary(t *testing.T) {
	tmp := t.TempDir()
	exe := filepath.Join(tmp, "cove")
	entitlements, err := filepath.Abs(filepath.Join("internal", "autosign", "vz.entitlements"))
	if err != nil {
		t.Fatalf("resolve entitlements: %v", err)
	}

	build := exec.Command("go", "build", "-o", exe, ".")
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	sign := exec.Command("codesign", "-s", "-", "-f", "--entitlements", entitlements, exe)
	sign.Dir = "."
	if out, err := sign.CombinedOutput(); err != nil {
		t.Fatalf("codesign: %v\n%s", err, out)
	}

	before := snapshotFile(t, exe)

	cmd := exec.Command(exe, "helper", "status")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper status: %v\n%s", err, out)
	}

	after := snapshotFile(t, exe)
	if !bytes.Equal(before.hash[:], after.hash[:]) || before.inode != after.inode {
		t.Fatalf("helper command rewrote binary\nbefore: inode=%d hash=%x\nafter:  inode=%d hash=%x\noutput:\n%s",
			before.inode, before.hash, after.inode, after.hash, out)
	}

	verify := exec.Command("codesign", "-vvv", exe)
	verify.Dir = "."
	if out, err := verify.CombinedOutput(); err != nil {
		t.Fatalf("codesign verify: %v\n%s", err, out)
	}
}

type fileSnapshot struct {
	hash  [32]byte
	inode uint64
}

func snapshotFile(t *testing.T, path string) fileSnapshot {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("stat payload for %s: %T", path, info.Sys())
	}

	return fileSnapshot{
		hash:  sha256.Sum256(data),
		inode: stat.Ino,
	}
}
