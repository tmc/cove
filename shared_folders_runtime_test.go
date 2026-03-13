package main

import "testing"

func TestHandleSharedFoldersApplyRequiresVM(t *testing.T) {
	s := NewControlServerWithVMDir("/tmp/nonexistent.sock", t.TempDir())
	resp := s.handleSharedFoldersApply()
	if resp.Success {
		t.Fatalf("expected failure when VM is not initialized")
	}
	if resp.Error == "" {
		t.Fatalf("expected error message when VM is not initialized")
	}
}
