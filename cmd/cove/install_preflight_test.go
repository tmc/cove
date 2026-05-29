package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallPreflightReadsLocalISO(t *testing.T) {
	oldISO, oldIPSW := isoPath, ipswPath
	t.Cleanup(func() {
		isoPath = oldISO
		ipswPath = oldIPSW
	})
	isoPath = filepath.Join(t.TempDir(), "install.iso")
	ipswPath = ""
	if err := os.WriteFile(isoPath, []byte("iso"), 0600); err != nil {
		t.Fatalf("write ISO: %v", err)
	}

	var out bytes.Buffer
	if err := runInstallPreflight(commandEnv{Stdout: &out, Stderr: new(bytes.Buffer)}); err != nil {
		t.Fatalf("runInstallPreflight: %v", err)
	}
	if !strings.Contains(out.String(), "install preflight: iso readable: "+isoPath) {
		t.Fatalf("preflight output = %q", out.String())
	}
}

func TestInstallPreflightRequiresMedia(t *testing.T) {
	oldISO, oldIPSW := isoPath, ipswPath
	t.Cleanup(func() {
		isoPath = oldISO
		ipswPath = oldIPSW
	})
	isoPath = ""
	ipswPath = ""
	if err := runInstallPreflight(commandEnv{Stdout: new(bytes.Buffer), Stderr: new(bytes.Buffer)}); err == nil || !strings.Contains(err.Error(), "requires -iso or -ipsw") {
		t.Fatalf("runInstallPreflight error = %v, want media requirement", err)
	}
}
