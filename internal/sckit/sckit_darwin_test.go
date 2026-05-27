//go:build darwin

package sckit

import (
	"strings"
	"testing"
)

func TestReadMacOSVersion(t *testing.T) {
	got := readMacOSVersion()
	if got == "" {
		t.Fatal("readMacOSVersion() = empty, want host version")
	}
	if strings.TrimSpace(got) != got {
		t.Fatalf("readMacOSVersion() = %q, want trimmed", got)
	}
}
