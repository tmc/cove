package main

import (
	"path/filepath"
	"testing"

	"github.com/tmc/vz-macos/internal/control/operations"
	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

// withTestOpsRegistry installs a fresh file-backed operations registry under
// a t.TempDir for the given ControlServer. Returns the registry so callers can
// pre-populate it.
func withTestOpsRegistry(t *testing.T, s *ControlServer) *operations.OperationRegistry {
	t.Helper()
	dir := t.TempDir()
	store, err := operations.NewFileOperationStore(filepath.Join(dir, "operations"))
	if err != nil {
		t.Fatalf("NewFileOperationStore: %v", err)
	}
	reg, err := operations.NewOperationRegistry(store)
	if err != nil {
		t.Fatalf("NewOperationRegistry: %v", err)
	}
	s.opsReg = reg
	return reg
}

// TestHandleOperationsList covers acceptance #5: ctl operations list must
// surface ops created by other code paths (here: pre-populated through the
// same registry).
func TestHandleOperationsList(t *testing.T) {
	cs := stubControlServer(t)
	reg := withTestOpsRegistry(t, cs)

	op1, err := reg.Create("snapshots/a")
	if err != nil {
		t.Fatalf("Create a: %v", err)
	}
	if err := reg.Start(op1.ID); err != nil {
		t.Fatalf("Start a: %v", err)
	}
	op2, err := reg.Create("snapshots/b")
	if err != nil {
		t.Fatalf("Create b: %v", err)
	}
	if err := reg.Succeed(op2.ID, map[string]any{"snapshot": "b"}); err != nil {
		t.Fatalf("Succeed b: %v", err)
	}

	resp := cs.handleOperationsCommand(&controlpb.OperationsCommand{Action: "list"})
	if !resp.Success || resp.Error != "" {
		t.Fatalf("list: success=%v err=%q", resp.Success, resp.Error)
	}
	listed := resp.GetOperationsList()
	if listed == nil || len(listed.Operations) != 2 {
		t.Fatalf("want 2 ops, got %+v", listed)
	}

	gotByID := make(map[string]*controlpb.OperationInfo, len(listed.Operations))
	for _, info := range listed.Operations {
		gotByID[info.Id] = info
	}
	if gotByID[op1.ID] == nil || gotByID[op1.ID].Status != "running" {
		t.Errorf("op a status = %+v, want running", gotByID[op1.ID])
	}
	if gotByID[op2.ID] == nil || gotByID[op2.ID].Status != "succeeded" {
		t.Errorf("op b status = %+v, want succeeded", gotByID[op2.ID])
	}
}

// TestHandleOperationsGet covers the get-by-id path used by ctl operations
// get and by the polling loop in runOperationsWait.
func TestHandleOperationsGet(t *testing.T) {
	cs := stubControlServer(t)
	reg := withTestOpsRegistry(t, cs)

	op, err := reg.Create("snapshots/x")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := reg.Fail(op.ID, "snapshot_save", "disk full"); err != nil {
		t.Fatalf("Fail: %v", err)
	}

	resp := cs.handleOperationsCommand(&controlpb.OperationsCommand{Action: "get", Id: op.ID})
	if !resp.Success {
		t.Fatalf("get: %q", resp.Error)
	}
	info := resp.GetOperation()
	if info == nil {
		t.Fatal("nil operation")
	}
	if info.Id != op.ID || info.Status != "failed" || info.ErrorCode != "snapshot_save" || info.ErrorMessage != "disk full" {
		t.Errorf("info = %+v", info)
	}
}

// TestHandleOperationsGetMissing returns a clean error for unknown IDs.
func TestHandleOperationsGetMissing(t *testing.T) {
	cs := stubControlServer(t)
	withTestOpsRegistry(t, cs)

	resp := cs.handleOperationsCommand(&controlpb.OperationsCommand{Action: "get", Id: "op_nope"})
	if resp.Error == "" {
		t.Fatal("missing op should produce error")
	}
}

// TestHandleOperationsBadAction rejects unknown actions instead of silently
// returning an empty result — protects callers from typos.
func TestHandleOperationsBadAction(t *testing.T) {
	cs := stubControlServer(t)
	withTestOpsRegistry(t, cs)

	resp := cs.handleOperationsCommand(&controlpb.OperationsCommand{Action: "delete", Id: "op_x"})
	if resp.Error == "" {
		t.Fatal("unknown action should produce error")
	}
}

// TestEnsureOpsPersistsAcrossReinit verifies the file-backed registry recovers
// completed ops after a fresh ControlServer is constructed pointing at the
// same VM directory. This is the persistence half of acceptance #5.
func TestEnsureOpsPersistsAcrossReinit(t *testing.T) {
	dir := t.TempDir()
	cs1 := &ControlServer{authToken: "t", vmDir: dir}
	reg1, err := cs1.ensureOps()
	if err != nil {
		t.Fatalf("ensureOps cs1: %v", err)
	}
	op, err := reg1.Create("snapshots/persist")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := reg1.Succeed(op.ID, map[string]any{"snapshot": "persist"}); err != nil {
		t.Fatalf("Succeed: %v", err)
	}

	// Fresh server pointing at the same vmDir should re-read the same op.
	cs2 := &ControlServer{authToken: "t", vmDir: dir}
	resp := cs2.handleOperationsCommand(&controlpb.OperationsCommand{Action: "get", Id: op.ID})
	if !resp.Success {
		t.Fatalf("reloaded get: %q", resp.Error)
	}
	info := resp.GetOperation()
	if info == nil || info.Status != "succeeded" {
		t.Errorf("reloaded info = %+v, want succeeded", info)
	}
}

// TestEnsureOpsReapsOrphans verifies the FileOperationStore reaper kicks in
// on Load: an op left in "running" state by a prior process is rewritten as
// "failed" with code "server_restart" before it's visible to the new
// ControlServer. This protects ctl operations wait from blocking forever on
// an op that died with cove.
func TestEnsureOpsReapsOrphans(t *testing.T) {
	dir := t.TempDir()
	cs1 := &ControlServer{authToken: "t", vmDir: dir}
	reg1, err := cs1.ensureOps()
	if err != nil {
		t.Fatalf("ensureOps cs1: %v", err)
	}
	op, err := reg1.Create("snapshots/orphan")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := reg1.Start(op.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Simulate process restart: build a new ControlServer for the same dir.
	cs2 := &ControlServer{authToken: "t", vmDir: dir}
	resp := cs2.handleOperationsCommand(&controlpb.OperationsCommand{Action: "get", Id: op.ID})
	if !resp.Success {
		t.Fatalf("reloaded get: %q", resp.Error)
	}
	info := resp.GetOperation()
	if info == nil || info.Status != "failed" || info.ErrorCode != "server_restart" {
		t.Errorf("reloaded info = %+v, want failed/server_restart", info)
	}
}
