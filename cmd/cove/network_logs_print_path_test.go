package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrintNetworkAuditPathReadError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.log")
	err := printNetworkAuditPath(&bytes.Buffer{}, missing)
	if err == nil || !strings.Contains(err.Error(), "network logs: read") {
		t.Fatalf("err = %v, want network logs: read", err)
	}
}

func TestPrintNetworkAuditPathWriteError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	if err := os.WriteFile(path, []byte("entry\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	w := &errWriter{err: errors.New("disk full")}
	err := printNetworkAuditPath(w, path)
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err = %v, want disk full", err)
	}
}

func TestPrintNetworkAuditPathHappy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	want := "two\nentries\n"
	if err := os.WriteFile(path, []byte(want), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	var out bytes.Buffer
	if err := printNetworkAuditPath(&out, path); err != nil {
		t.Fatalf("err = %v", err)
	}
	if out.String() != want {
		t.Fatalf("out = %q, want %q", out.String(), want)
	}
}
