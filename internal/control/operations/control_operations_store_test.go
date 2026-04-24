package operations

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

func newTestOp(id string) *Operation {
	now := time.Now().UTC().Truncate(time.Millisecond)
	return &Operation{
		ID:        id,
		Resource:  "vms/test-vm",
		Status:    "running",
		CreatedAt: now,
		UpdatedAt: now,
		Progress: &OperationProgress{
			Phase:   "install",
			Percent: 42,
			Message: "downloading",
		},
	}
}

func TestOperationStore(t *testing.T) {
	t.Run("SaveLoadRoundTrip", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewFileOperationStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		op := newTestOp("op_1")
		op.Status = "succeeded"
		op.Result = map[string]any{"vm": "test-vm"}

		if err := store.Save(op); err != nil {
			t.Fatal(err)
		}

		ops, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}
		if len(ops) != 1 {
			t.Fatalf("want 1 op, got %d", len(ops))
		}
		got := ops[0]
		if got.ID != op.ID {
			t.Errorf("ID: want %q got %q", op.ID, got.ID)
		}
		if got.Resource != op.Resource {
			t.Errorf("Resource: want %q got %q", op.Resource, got.Resource)
		}
		if got.Status != op.Status {
			t.Errorf("Status: want %q got %q", op.Status, got.Status)
		}
		if !got.CreatedAt.Equal(op.CreatedAt) {
			t.Errorf("CreatedAt: want %v got %v", op.CreatedAt, got.CreatedAt)
		}
		if got.Progress == nil || got.Progress.Percent != 42 {
			t.Errorf("Progress not preserved: %+v", got.Progress)
		}
		if got.Result["vm"] != "test-vm" {
			t.Errorf("Result not preserved: %v", got.Result)
		}
	})

	t.Run("PersistAcrossReload", func(t *testing.T) {
		dir := t.TempDir()
		store1, err := NewFileOperationStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		op := newTestOp("op_persist")
		op.Status = "succeeded"
		if err := store1.Save(op); err != nil {
			t.Fatal(err)
		}
		// simulate crash: do NOT call anything else on store1

		store2, err := NewFileOperationStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		ops, err := store2.Load()
		if err != nil {
			t.Fatal(err)
		}
		if len(ops) != 1 {
			t.Fatalf("want 1 op, got %d", len(ops))
		}
		if ops[0].ID != "op_persist" {
			t.Errorf("unexpected id %q", ops[0].ID)
		}
	})

	t.Run("OrphanedOpsMarkedFailed", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewFileOperationStore(dir)
		if err != nil {
			t.Fatal(err)
		}

		for _, status := range []string{"pending", "running"} {
			op := newTestOp("op_orphan_" + status)
			op.Status = status
			if err := store.Save(op); err != nil {
				t.Fatal(err)
			}
		}

		ops, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}
		if len(ops) != 2 {
			t.Fatalf("want 2 ops, got %d", len(ops))
		}
		for _, op := range ops {
			if op.Status != "failed" {
				t.Errorf("op %s: want status=failed, got %q", op.ID, op.Status)
			}
			if op.Error == nil || op.Error.Code != "server_restart" {
				t.Errorf("op %s: want error.code=server_restart, got %+v", op.ID, op.Error)
			}
		}
	})

	t.Run("ConcurrentSaveLoad", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewFileOperationStore(dir)
		if err != nil {
			t.Fatal(err)
		}

		const goroutines = 50
		const opsPerGoroutine = 20
		var wg sync.WaitGroup
		wg.Add(goroutines)

		for g := 0; g < goroutines; g++ {
			g := g
			go func() {
				defer wg.Done()
				for i := 0; i < opsPerGoroutine; i++ {
					id := "op_g" + strconv.Itoa(g) + "_i" + strconv.Itoa(i)
					op := newTestOp(id)
					op.Status = "succeeded"
					if err := store.Save(op); err != nil {
						t.Errorf("save %s: %v", id, err)
					}
				}
			}()
		}
		wg.Wait()

		// Concurrent loads must never return torn JSON.
		var loadWg sync.WaitGroup
		for i := 0; i < 10; i++ {
			loadWg.Add(1)
			go func() {
				defer loadWg.Done()
				if _, err := store.Load(); err != nil {
					t.Errorf("concurrent load error: %v", err)
				}
			}()
		}
		loadWg.Wait()
	})

	t.Run("TmpFilesIgnored", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewFileOperationStore(dir)
		if err != nil {
			t.Fatal(err)
		}

		// Write a .tmp file that looks like a partial write.
		tmpPath := filepath.Join(dir, "op_partial.json.tmp")
		if err := os.WriteFile(tmpPath, []byte("{broken json"), 0600); err != nil {
			t.Fatal(err)
		}

		ops, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}
		if len(ops) != 0 {
			t.Errorf("want 0 ops, got %d", len(ops))
		}
	})

	t.Run("PurgeOlderThan", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewFileOperationStore(dir)
		if err != nil {
			t.Fatal(err)
		}

		old := time.Now().Add(-2 * time.Hour)

		// old terminal ops — should be purged
		for _, id := range []string{"op_old_succ", "op_old_fail"} {
			op := newTestOp(id)
			op.Status = map[string]string{
				"op_old_succ": "succeeded",
				"op_old_fail": "failed",
			}[id]
			op.UpdatedAt = old
			if err := store.Save(op); err != nil {
				t.Fatal(err)
			}
		}

		// recent terminal op — should survive
		opRecent := newTestOp("op_recent_succ")
		opRecent.Status = "succeeded"
		if err := store.Save(opRecent); err != nil {
			t.Fatal(err)
		}

		// old pending / running — should survive regardless of age
		for _, id := range []string{"op_old_pending", "op_old_running"} {
			op := newTestOp(id)
			op.Status = map[string]string{
				"op_old_pending": "pending",
				"op_old_running": "running",
			}[id]
			op.UpdatedAt = old
			if err := store.Save(op); err != nil {
				t.Fatal(err)
			}
		}

		purged, err := store.PurgeOlderThan(time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if purged != 2 {
			t.Errorf("want 2 purged, got %d", purged)
		}

		// Verify remaining files.
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		var remaining []string
		for _, e := range entries {
			if filepath.Ext(e.Name()) == ".json" {
				remaining = append(remaining, e.Name())
			}
		}
		if len(remaining) != 3 {
			t.Errorf("want 3 remaining, got %d: %v", len(remaining), remaining)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewFileOperationStore(dir)
		if err != nil {
			t.Fatal(err)
		}

		op := newTestOp("op_to_delete")
		op.Status = "succeeded"
		if err := store.Save(op); err != nil {
			t.Fatal(err)
		}

		if err := store.Delete("op_to_delete"); err != nil {
			t.Fatal(err)
		}

		if _, err := os.Stat(filepath.Join(dir, "op_to_delete.json")); !os.IsNotExist(err) {
			t.Errorf("file should not exist after delete")
		}

		ops, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}
		if len(ops) != 0 {
			t.Errorf("want 0 ops after delete, got %d", len(ops))
		}
	})

	t.Run("SortedByCreatedAt", func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewFileOperationStore(dir)
		if err != nil {
			t.Fatal(err)
		}

		base := time.Now()
		for i := 4; i >= 0; i-- {
			op := newTestOp("op_sort_" + strconv.Itoa(i))
			op.Status = "succeeded"
			op.CreatedAt = base.Add(time.Duration(i) * time.Second)
			op.UpdatedAt = op.CreatedAt
			if err := store.Save(op); err != nil {
				t.Fatal(err)
			}
		}

		ops, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}
		for i := 1; i < len(ops); i++ {
			if ops[i].CreatedAt.Before(ops[i-1].CreatedAt) {
				t.Errorf("ops not sorted: ops[%d].CreatedAt=%v before ops[%d].CreatedAt=%v",
					i, ops[i].CreatedAt, i-1, ops[i-1].CreatedAt)
			}
		}
	})
}

// TestMemOperationStore exercises the in-memory store used in tests.
func TestMemOperationStore(t *testing.T) {
	store := NewMemOperationStore()

	op := newTestOp("op_mem_1")
	op.Status = "succeeded"
	if err := store.Save(op); err != nil {
		t.Fatal(err)
	}

	ops, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].ID != "op_mem_1" {
		t.Fatalf("unexpected ops: %+v", ops)
	}

	if err := store.Delete("op_mem_1"); err != nil {
		t.Fatal(err)
	}
	ops, _ = store.Load()
	if len(ops) != 0 {
		t.Errorf("want 0 after delete, got %d", len(ops))
	}
}

// Verify JSON round-trip fidelity for all fields.
func TestOperationJSONRoundTrip(t *testing.T) {
	op := &Operation{
		ID:        "op_json",
		Resource:  "vms/my-vm",
		Status:    "failed",
		CreatedAt: time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 4, 16, 12, 5, 0, 0, time.UTC),
		Progress:  &OperationProgress{Phase: "install", Percent: 80, Message: "nearly done"},
		Error:     &OperationError{Code: "timeout", Message: "timed out waiting for vm"},
	}

	data, err := json.Marshal(op)
	if err != nil {
		t.Fatal(err)
	}
	var got Operation
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Error.Code != "timeout" {
		t.Errorf("Error.Code: want timeout, got %q", got.Error.Code)
	}
	if got.Progress.Percent != 80 {
		t.Errorf("Progress.Percent: want 80, got %d", got.Progress.Percent)
	}
}
