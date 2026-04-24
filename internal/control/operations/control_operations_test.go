package operations

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestRegistry(t *testing.T) *OperationRegistry {
	t.Helper()
	r, err := NewOperationRegistry(NewMemOperationStore())
	if err != nil {
		t.Fatalf("NewOperationRegistry: %v", err)
	}
	return r
}

func TestCreate_IDFormat(t *testing.T) {
	r := newTestRegistry(t)
	op, err := r.Create("vm/test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.HasPrefix(op.ID, "op_") {
		t.Errorf("ID %q missing op_ prefix", op.ID)
	}
	if len(op.ID) != len("op_")+8 {
		t.Errorf("ID %q wrong length (want 11, got %d)", op.ID, len(op.ID))
	}
	// validate hex suffix
	for _, c := range op.ID[3:] {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Errorf("ID %q has non-hex char %q", op.ID, c)
		}
	}
}

func TestCreate_InitialState(t *testing.T) {
	r := newTestRegistry(t)
	before := time.Now().Add(-time.Millisecond)
	op, err := r.Create("vm/alpha")
	after := time.Now().Add(time.Millisecond)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if op.Status != "pending" {
		t.Errorf("status = %q, want pending", op.Status)
	}
	if op.Resource != "vm/alpha" {
		t.Errorf("resource = %q, want vm/alpha", op.Resource)
	}
	if op.CreatedAt.Before(before) || op.CreatedAt.After(after) {
		t.Errorf("CreatedAt %v outside expected range", op.CreatedAt)
	}
	if op.UpdatedAt.IsZero() {
		t.Error("UpdatedAt is zero")
	}
}

func TestGetList(t *testing.T) {
	r := newTestRegistry(t)
	a, _ := r.Create("vm/a")
	b, _ := r.Create("vm/b")

	got, ok := r.Get(a.ID)
	if !ok {
		t.Fatal("Get returned false for existing op")
	}
	if got.ID != a.ID {
		t.Errorf("Get ID mismatch: %s != %s", got.ID, a.ID)
	}

	_, ok = r.Get("op_00000000")
	if ok {
		t.Error("Get returned true for nonexistent op")
	}

	list := r.List()
	if len(list) != 2 {
		t.Fatalf("List len = %d, want 2", len(list))
	}
	if list[0].ID != a.ID || list[1].ID != b.ID {
		t.Error("List not sorted by CreatedAt")
	}
}

func TestOperationRegistry_StatusTransition(t *testing.T) {
	r := newTestRegistry(t)
	op, _ := r.Create("vm/x")

	if err := r.Start(op.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := r.SetProgress(op.ID, OperationProgress{Phase: "start", Percent: 10}); err != nil {
		t.Fatalf("SetProgress: %v", err)
	}
	got, _ := r.Get(op.ID)
	if got.Status != "running" {
		t.Errorf("status = %q, want running", got.Status)
	}
	if got.Progress == nil || got.Progress.Percent != 10 {
		t.Error("Progress not updated")
	}
}

func TestOperationRegistryTransitions(t *testing.T) {
	r := newTestRegistry(t)
	op, _ := r.Create("vm/transitions")

	if err := r.Start(op.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	got, _ := r.Get(op.ID)
	if got.Status != operationStatusRunning {
		t.Errorf("status = %q, want %q", got.Status, operationStatusRunning)
	}

	if err := r.SetProgress(op.ID, OperationProgress{Phase: "install", Percent: 40, Message: "copying"}); err != nil {
		t.Fatalf("SetProgress: %v", err)
	}
	got, _ = r.Get(op.ID)
	if got.Progress == nil || got.Progress.Phase != "install" || got.Progress.Percent != 40 {
		t.Errorf("progress = %+v, want install/40", got.Progress)
	}

	result := map[string]any{"ip": "192.168.64.2"}
	if err := r.Succeed(op.ID, result); err != nil {
		t.Fatalf("Succeed: %v", err)
	}
	result["ip"] = "changed"
	got, _ = r.Get(op.ID)
	if got.Status != operationStatusSucceeded {
		t.Errorf("status = %q, want %q", got.Status, operationStatusSucceeded)
	}
	if got.Result["ip"] != "192.168.64.2" {
		t.Errorf("result ip = %v, want 192.168.64.2", got.Result["ip"])
	}

	failed, _ := r.Create("vm/failed")
	if err := r.Fail(failed.ID, "boom", "failed for test"); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	got, _ = r.Get(failed.ID)
	if got.Status != operationStatusFailed {
		t.Errorf("status = %q, want %q", got.Status, operationStatusFailed)
	}
	if got.Error == nil || got.Error.Code != "boom" || got.Error.Message != "failed for test" {
		t.Errorf("error = %+v, want boom", got.Error)
	}
}

func TestOperationRegistry_PersistsViaStore(t *testing.T) {
	store := NewMemOperationStore()
	r, _ := NewOperationRegistry(store)
	op, _ := r.Create("vm/persist")

	if err := r.Succeed(op.ID, map[string]any{"ip": "192.168.1.1"}); err != nil {
		t.Fatalf("Succeed: %v", err)
	}

	// Load directly from store to confirm persistence.
	ops, _ := store.Load()
	var found *Operation
	for _, o := range ops {
		if o.ID == op.ID {
			found = o
		}
	}
	if found == nil {
		t.Fatal("op not found in store after Succeed")
	}
	if found.Status != "succeeded" {
		t.Errorf("stored status = %q, want succeeded", found.Status)
	}
}

func TestOperationRegistry_NonExistentTransition(t *testing.T) {
	r := newTestRegistry(t)
	err := r.Start("op_deadbeef")
	if !errors.Is(err, ErrOperationNotFound) {
		t.Errorf("expected ErrOperationNotFound, got %v", err)
	}
}

func TestSubscribe_ReceivesUpdate(t *testing.T) {
	r := newTestRegistry(t)
	op, _ := r.Create("vm/sub")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ch, err := r.Subscribe(ctx, op.ID)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	go r.Start(op.ID)

	select {
	case snap, ok := <-ch:
		if !ok {
			t.Fatal("channel closed before receiving event")
		}
		if snap.Status != "running" {
			t.Errorf("snap.Status = %q, want running", snap.Status)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for subscription event")
	}
}

func TestSubscribe_TerminalOpReturnsClosed(t *testing.T) {
	r := newTestRegistry(t)
	op, _ := r.Create("vm/done")
	if err := r.Succeed(op.ID, nil); err != nil {
		t.Fatalf("Succeed: %v", err)
	}

	ch, err := r.Subscribe(context.Background(), op.ID)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	// Channel should already be closed.
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("expected closed channel, got value")
		}
	default:
		t.Error("channel not closed for terminal op")
	}
}

func TestSubscribe_NonExistent(t *testing.T) {
	r := newTestRegistry(t)
	_, err := r.Subscribe(context.Background(), "op_00000000")
	if !errors.Is(err, ErrOperationNotFound) {
		t.Errorf("expected ErrOperationNotFound, got %v", err)
	}
}

func TestSubscribe_TerminalClosesChannel(t *testing.T) {
	r := newTestRegistry(t)
	op, _ := r.Create("vm/term")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ch, _ := r.Subscribe(ctx, op.ID)
	if err := r.Fail(op.ID, "test_failed", "failed for test"); err != nil {
		t.Fatalf("Fail: %v", err)
	}

	// Drain until closed.
	for range ch {
	}
	// If we get here the channel was closed — success.
}

func TestSubscribe_CtxCancelRemovesSub(t *testing.T) {
	r := newTestRegistry(t)
	op, _ := r.Create("vm/ctx")

	ctx, cancel := context.WithCancel(context.Background())
	ch, _ := r.Subscribe(ctx, op.ID)
	cancel()

	// After cancel the channel should close (give goroutine time to run).
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("expected channel closed after ctx cancel")
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("channel not closed after ctx cancel")
	}
}

func TestConcurrent_NoDeadlock(t *testing.T) {
	r := newTestRegistry(t)
	op, _ := r.Create("vm/race")

	var wg sync.WaitGroup
	const n = 100

	// 100 updaters.
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r.SetProgress(op.ID, OperationProgress{Percent: i % 100})
		}(i)
	}

	// 100 subscribers.
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			ch, err := r.Subscribe(ctx, op.ID)
			if err != nil {
				if errors.Is(err, ErrOperationNotFound) {
					return // op may have been purged
				}
				return
			}
			for range ch {
			}
		}()
	}

	wg.Wait()
}

func TestSlowSubscriber_RegistryNeverBlocks(t *testing.T) {
	r := newTestRegistry(t)
	op, _ := r.Create("vm/slow")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ch, _ := r.Subscribe(ctx, op.ID)

	// Fire 20 updates without reading from ch (buffer is 16).
	for i := 0; i < 20; i++ {
		r.SetProgress(op.ID, OperationProgress{Percent: 50})
	}
	// Mark terminal so we can drain cleanly.
	if err := r.Succeed(op.ID, nil); err != nil {
		t.Fatalf("Succeed: %v", err)
	}

	// Drain channel — it must be closed eventually.
	select {
	case <-time.After(1 * time.Second):
		t.Error("channel not closed after terminal update (blocked?)")
	case _, ok := <-ch:
		if ok {
			// At least one event received; drain fully.
			for range ch {
			}
		}
	}
}

func TestPurgeOlderThan(t *testing.T) {
	r := newTestRegistry(t)
	a, _ := r.Create("vm/a")
	b, _ := r.Create("vm/b")
	c, _ := r.Create("vm/c") // running, should NOT be purged

	if err := r.Succeed(a.ID, nil); err != nil {
		t.Fatalf("Succeed a: %v", err)
	}
	if err := r.Succeed(b.ID, nil); err != nil {
		t.Fatalf("Succeed b: %v", err)
	}
	if err := r.Start(c.ID); err != nil {
		t.Fatalf("Start c: %v", err)
	}

	// Manually backdate the in-memory UpdatedAt for a and b.
	// Transitions set UpdatedAt = now; this test needs old terminal operations.
	r.mu.Lock()
	r.ops[a.ID].op.UpdatedAt = time.Now().Add(-2 * time.Hour)
	r.ops[b.ID].op.UpdatedAt = time.Now().Add(-2 * time.Hour)
	r.mu.Unlock()

	n, err := r.PurgeOlderThan(time.Hour)
	if err != nil {
		t.Fatalf("PurgeOlderThan: %v", err)
	}
	if n != 2 {
		t.Errorf("purged %d ops, want 2", n)
	}
	if _, ok := r.Get(a.ID); ok {
		t.Error("op a should be purged")
	}
	if _, ok := r.Get(b.ID); ok {
		t.Error("op b should be purged")
	}
	if _, ok := r.Get(c.ID); !ok {
		t.Error("running op c should NOT be purged")
	}
}

func TestLoadFromStore_ReindexesPriorOps(t *testing.T) {
	// Use a temp FileOperationStore so Load() coerces in-flight ops to failed.
	dir := t.TempDir()
	store, err := NewFileOperationStore(dir)
	if err != nil {
		t.Fatalf("NewFileOperationStore: %v", err)
	}
	r1, _ := NewOperationRegistry(store)
	op, _ := r1.Create("vm/reload")
	if err := r1.Start(op.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Simulate process restart: a new registry calling Load() coerces in-flight ops.
	store2, _ := NewFileOperationStore(dir)
	r2, err := NewOperationRegistry(store2)
	if err != nil {
		t.Fatalf("NewOperationRegistry (reload): %v", err)
	}
	got, ok := r2.Get(op.ID)
	if !ok {
		t.Fatal("op not found after reload")
	}
	// FileOperationStore.Load() marks in-flight ops as failed with server_restart.
	if got.Status != "failed" {
		t.Errorf("reloaded status = %q, want failed", got.Status)
	}
	if got.Error == nil || got.Error.Code != "server_restart" {
		t.Error("expected server_restart error code after reload")
	}
}

// TestFileStore_DurabilityAcrossRestart covers the richer durability case:
// a succeeded op round-trips with Result and Progress intact, a running op
// in the same directory is failed with server_restart, and both survive a
// process restart simulated by constructing a fresh store+registry.
func TestFileStore_DurabilityAcrossRestart(t *testing.T) {
	dir := t.TempDir()

	store, err := NewFileOperationStore(dir)
	if err != nil {
		t.Fatalf("NewFileOperationStore: %v", err)
	}
	r1, err := NewOperationRegistry(store)
	if err != nil {
		t.Fatalf("NewOperationRegistry: %v", err)
	}

	doneOp, _ := r1.Create("vm/done")
	if err := r1.SetProgress(doneOp.ID, OperationProgress{Phase: "finalize", Percent: 100}); err != nil {
		t.Fatalf("SetProgress done: %v", err)
	}
	if err := r1.Succeed(doneOp.ID, map[string]any{"ip": "10.0.0.1", "disk_bytes": float64(1024)}); err != nil {
		t.Fatalf("Succeed done: %v", err)
	}

	runOp, _ := r1.Create("vm/running")
	if err := r1.Start(runOp.ID); err != nil {
		t.Fatalf("Start running: %v", err)
	}
	if err := r1.SetProgress(runOp.ID, OperationProgress{Phase: "install", Percent: 42}); err != nil {
		t.Fatalf("SetProgress running: %v", err)
	}

	// Simulate process restart — build a new store+registry over the same dir.
	store2, err := NewFileOperationStore(dir)
	if err != nil {
		t.Fatalf("NewFileOperationStore (restart): %v", err)
	}
	r2, err := NewOperationRegistry(store2)
	if err != nil {
		t.Fatalf("NewOperationRegistry (restart): %v", err)
	}

	gotDone, ok := r2.Get(doneOp.ID)
	if !ok {
		t.Fatal("succeeded op missing after restart")
	}
	if gotDone.Status != "succeeded" {
		t.Errorf("done status = %q, want succeeded", gotDone.Status)
	}
	if gotDone.Progress == nil || gotDone.Progress.Percent != 100 {
		t.Errorf("done progress not preserved: %+v", gotDone.Progress)
	}
	if ip, _ := gotDone.Result["ip"].(string); ip != "10.0.0.1" {
		t.Errorf("done result.ip = %v, want 10.0.0.1", gotDone.Result["ip"])
	}

	gotRun, ok := r2.Get(runOp.ID)
	if !ok {
		t.Fatal("running op missing after restart")
	}
	if gotRun.Status != "failed" {
		t.Errorf("running-after-restart status = %q, want failed", gotRun.Status)
	}
	if gotRun.Error == nil || gotRun.Error.Code != "server_restart" {
		t.Errorf("running-after-restart error = %+v, want code=server_restart", gotRun.Error)
	}

	// A third cycle must be idempotent: the failed op must stay failed.
	store3, _ := NewFileOperationStore(dir)
	r3, err := NewOperationRegistry(store3)
	if err != nil {
		t.Fatalf("NewOperationRegistry (third): %v", err)
	}
	again, _ := r3.Get(runOp.ID)
	if again.Status != "failed" || again.Error == nil || again.Error.Code != "server_restart" {
		t.Errorf("third-load op = %+v, want stable failed/server_restart", again)
	}
}
