//go:build unix

package iosbundle

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestValidatePreparedRejectsFIFO(t *testing.T) {
	dir, cfg := preparedFixture(t)
	cfg.ROM = "pipe"
	if err := syscall.Mkfifo(filepath.Join(dir, cfg.ROM), 0600); err != nil {
		t.Fatal(err)
	}
	if err := cfg.ValidatePrepared(dir); err == nil || !strings.Contains(err.Error(), `ios file "pipe": expected a nonempty regular file`) {
		t.Fatalf("ValidatePrepared() = %v", err)
	}
}
