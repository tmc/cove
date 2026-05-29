package main

import (
	"strings"
	"testing"
)

func TestHandlePITSwapNoVM(t *testing.T) {
	s := NewControlServerWithVMDir("", t.TempDir())
	resp := s.handlePITSwap("checkpoint", false)
	if resp == nil {
		t.Fatal("nil response")
	}
	if !strings.Contains(resp.Error, "vm not configured") {
		t.Fatalf("Error = %q, want substring %q", resp.Error, "vm not configured")
	}
}
