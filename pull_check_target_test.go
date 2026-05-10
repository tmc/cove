package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckPullTargetNoDisk(t *testing.T) {
	dir := t.TempDir()
	if err := checkPullTarget(dir); err != nil {
		t.Errorf("checkPullTarget(empty dir) = %v, want nil", err)
	}
}

func TestCheckPullTargetIncompletePartialOnly(t *testing.T) {
	dir := t.TempDir()
	partial := filepath.Join(dir, "disk.img.partial")
	if err := os.WriteFile(partial, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	err := checkPullTarget(dir)
	if err == nil || !strings.Contains(err.Error(), "incomplete disk") {
		t.Errorf("checkPullTarget(partial only) = %v, want incomplete disk error", err)
	}
}

func TestCheckPullTargetDiskNoPartial(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "disk.img"), []byte("d"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := checkPullTarget(dir); err != nil {
		t.Errorf("checkPullTarget(disk only) = %v, want nil", err)
	}
}

func TestCheckPullTargetDiskAndPartial(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "disk.img"), []byte("d"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "disk.img.partial"), []byte("p"), 0600); err != nil {
		t.Fatal(err)
	}
	err := checkPullTarget(dir)
	if err == nil || !strings.Contains(err.Error(), "incomplete disk") {
		t.Errorf("checkPullTarget(disk+partial) = %v, want incomplete disk error", err)
	}
}
