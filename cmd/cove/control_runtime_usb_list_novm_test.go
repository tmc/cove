package main

import (
	"strings"
	"testing"
)

func TestRuntimeUSBListNoVM(t *testing.T) {
	s := NewControlServerWithVMDir("", t.TempDir())
	_, err := s.runtimeUSBList()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "vm not configured") {
		t.Fatalf("err = %q, want substring %q", err.Error(), "vm not configured")
	}
}
