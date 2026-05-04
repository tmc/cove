package main

import (
	"encoding/json"
	"testing"
)

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

func TestHandleSharedFoldersRuntimeStatusWithoutVM(t *testing.T) {
	s := NewControlServerWithVMDir("/tmp/nonexistent.sock", t.TempDir())
	resp := s.handleSharedFoldersRuntimeStatus()
	if !resp.Success {
		t.Fatalf("handleSharedFoldersRuntimeStatus() success = false: %s", resp.Error)
	}
	var status sharedFoldersRuntimeStatus
	if err := json.Unmarshal([]byte(resp.Data), &status); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	if status.Running || status.VirtioFS {
		t.Fatalf("status = %+v, want no running VM and no virtiofs", status)
	}
	if status.Message == "" {
		t.Fatalf("status message is empty")
	}
}
