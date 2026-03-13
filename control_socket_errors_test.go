package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatControlSocketDialErrorNoSocket(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "macos-3", "control.sock")
	err := formatControlSocketDialError(sock, os.ErrNotExist)
	if err == nil {
		t.Fatalf("formatControlSocketDialError() returned nil")
	}
	got := err.Error()
	if !strings.Contains(got, "vm is not running") {
		t.Fatalf("missing not-running hint: %q", got)
	}
	if !strings.Contains(got, "vz-macos -vm macos-3 run") {
		t.Fatalf("missing vm-specific run hint: %q", got)
	}
}

func TestFormatControlSocketDialErrorConnectionRefused(t *testing.T) {
	base := t.TempDir()
	sock := filepath.Join(base, "macos-3", "control.sock")
	if err := os.MkdirAll(filepath.Dir(sock), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A regular file is enough to model "path exists" for stale socket messaging.
	if err := os.WriteFile(sock, []byte("x"), 0600); err != nil {
		t.Fatalf("write socket placeholder: %v", err)
	}

	dialErr := errors.New("dial unix /tmp/control.sock: connect: connection refused")
	err := formatControlSocketDialError(sock, dialErr)
	if err == nil {
		t.Fatalf("formatControlSocketDialError() returned nil")
	}
	got := err.Error()
	if !strings.Contains(got, "may still be booting") {
		t.Fatalf("missing booting hint: %q", got)
	}
	if !strings.Contains(got, "remove "+sock) {
		t.Fatalf("missing stale socket cleanup hint: %q", got)
	}
}

func TestFormatControlSocketDialErrorTimeout(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "control.sock")
	err := formatControlSocketDialError(sock, errors.New("dial unix /tmp/control.sock: i/o timeout"))
	if err == nil {
		t.Fatalf("formatControlSocketDialError() returned nil")
	}
	got := err.Error()
	if !strings.Contains(got, "not ready") {
		t.Fatalf("missing not-ready hint: %q", got)
	}
	if !strings.Contains(got, "booting") {
		t.Fatalf("missing booting hint: %q", got)
	}
}
