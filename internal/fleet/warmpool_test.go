// cove fleet control plane (paid layer) — see docs/designs/046-fleet-control-plane.md
package fleet

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// fakeForker records fork calls and lets a test control success/failure and the
// returned placement.
type fakeForker struct {
	mu    sync.Mutex
	calls int
	fail  bool
}

func (f *fakeForker) Fork(ctx context.Context, ref string) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	n := f.calls
	if f.fail {
		return "", "", errors.New("fork boom")
	}
	return "hostX", fmt.Sprintf("%s-vm-%d", ref, n), nil
}

func (f *fakeForker) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// markAllReady advances every Warming member to Ready, simulating the
// controller's vsock-agent probe completing.
func markAllReady(t *testing.T, p *WarmPool) {
	t.Helper()
	for _, m := range p.Members() {
		if m.State == WarmWarming {
			if err := p.MarkReady(m.ID); err != nil {
				t.Fatalf("MarkReady(%s): %v", m.ID, err)
			}
		}
	}
}

func TestWarmPoolReconcileForksToTarget(t *testing.T) {
	f := &fakeForker{}
	p := NewWarmPool(f)
	if err := p.SetQuota(WarmConfig{Ref: "base:14.5", TargetReady: 3}); err != nil {
		t.Fatalf("SetQuota: %v", err)
	}
	started, err := p.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if started != 3 {
		t.Fatalf("started = %d, want 3", started)
	}
	// All three are Warming until their agents come up.
	markAllReady(t, p)
	if got := p.ReadyCount("base:14.5"); got != 3 {
		t.Fatalf("ready = %d, want 3", got)
	}
	// Reconcile is idempotent at target: no extra forks.
	started, err = p.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile (steady): %v", err)
	}
	if started != 0 {
		t.Fatalf("started at steady state = %d, want 0", started)
	}
}

func TestWarmPoolClaimReturnsReadyAndReplenishes(t *testing.T) {
	f := &fakeForker{}
	p := NewWarmPool(nil) // nil forker: drive Reconcile manually for determinism.
	p.forker = f
	if err := p.SetQuota(WarmConfig{Ref: "base", TargetReady: 2}); err != nil {
		t.Fatalf("SetQuota: %v", err)
	}
	if _, err := p.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	markAllReady(t, p)
	if p.ReadyCount("base") != 2 {
		t.Fatalf("ready = %d, want 2", p.ReadyCount("base"))
	}

	claimed, ok := p.Claim(context.Background(), "base")
	if !ok {
		t.Fatal("Claim should return a ready VM")
	}
	if claimed.HostID != "hostX" || claimed.VMName == "" {
		t.Fatalf("claimed = %+v, want a placed VM", claimed)
	}
	if claimed.Ref != "base" {
		t.Fatalf("claimed ref = %q, want base", claimed.Ref)
	}

	// After a claim, one ready remains and the pool is below target, so an
	// explicit Reconcile must refork the shortfall (the async one fired too).
	if _, err := p.Reconcile(context.Background()); err != nil {
		t.Fatalf("replenish Reconcile: %v", err)
	}
	markAllReady(t, p)
	if got := p.ReadyCount("base"); got != 2 {
		t.Fatalf("ready after replenish = %d, want 2", got)
	}
}

func TestWarmPoolClaimEmptyReturnsFalse(t *testing.T) {
	p := NewWarmPool(&fakeForker{})
	if err := p.SetQuota(WarmConfig{Ref: "base", TargetReady: 1}); err != nil {
		t.Fatalf("SetQuota: %v", err)
	}
	// No Reconcile yet: nothing ready.
	if _, ok := p.Claim(context.Background(), "base"); ok {
		t.Fatal("Claim should miss when no VM is ready")
	}
	// Forked but not yet ready (still Warming): still a miss.
	if _, err := p.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if _, ok := p.Claim(context.Background(), "base"); ok {
		t.Fatal("Claim should miss while member is only Warming, not Ready")
	}
}

func TestWarmPoolForkFailureFreesSlot(t *testing.T) {
	f := &fakeForker{fail: true}
	p := NewWarmPool(f)
	if err := p.SetQuota(WarmConfig{Ref: "base", TargetReady: 2}); err != nil {
		t.Fatalf("SetQuota: %v", err)
	}
	started, err := p.Reconcile(context.Background())
	if err == nil {
		t.Fatal("expected error when forks fail")
	}
	if started != 0 {
		t.Fatalf("started = %d, want 0 on all-fail", started)
	}
	// Members landed in Failed, so the next (succeeding) Reconcile retries the
	// full shortfall rather than treating failed members as pending capacity.
	f.fail = false
	started, err = p.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("retry Reconcile: %v", err)
	}
	if started != 2 {
		t.Fatalf("retry started = %d, want 2", started)
	}
}

func TestWarmPoolMarkReadyErrors(t *testing.T) {
	p := NewWarmPool(&fakeForker{})
	if err := p.MarkReady("nope"); err == nil {
		t.Fatal("MarkReady on unknown id should error")
	}
	if err := p.SetQuota(WarmConfig{Ref: "base", TargetReady: 1}); err != nil {
		t.Fatalf("SetQuota: %v", err)
	}
	if _, err := p.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	id := p.Members()[0].ID
	if err := p.MarkReady(id); err != nil {
		t.Fatalf("MarkReady(warming): %v", err)
	}
	// Marking an already-Ready member errors (not in Warming).
	if err := p.MarkReady(id); err == nil {
		t.Fatal("MarkReady on a Ready member should error")
	}
}

func TestWarmPoolSetQuotaValidation(t *testing.T) {
	p := NewWarmPool(nil)
	if err := p.SetQuota(WarmConfig{Ref: "", TargetReady: 1}); err == nil {
		t.Fatal("empty ref should error")
	}
	if err := p.SetQuota(WarmConfig{Ref: "base", TargetReady: -1}); err == nil {
		t.Fatal("negative target should error")
	}
}

func TestWarmPoolNilForkerNoop(t *testing.T) {
	p := NewWarmPool(nil)
	if err := p.SetQuota(WarmConfig{Ref: "base", TargetReady: 3}); err != nil {
		t.Fatalf("SetQuota: %v", err)
	}
	started, err := p.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if started != 0 {
		t.Fatalf("nil forker should start nothing, got %d", started)
	}
}

func TestWarmPoolConcurrentReconcileNoDoubleFork(t *testing.T) {
	f := &fakeForker{}
	p := NewWarmPool(f)
	if err := p.SetQuota(WarmConfig{Ref: "base", TargetReady: 4}); err != nil {
		t.Fatalf("SetQuota: %v", err)
	}
	var wg sync.WaitGroup
	var totalStarted atomic.Int64
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, _ := p.Reconcile(context.Background())
			totalStarted.Add(int64(n))
		}()
	}
	wg.Wait()
	// Across all concurrent Reconciles the pool must never overshoot the target:
	// total forks == target, never more.
	if got := f.callCount(); got != 4 {
		t.Fatalf("fork calls = %d, want exactly 4 (no double-fork)", got)
	}
	if totalStarted.Load() != 4 {
		t.Fatalf("total started = %d, want 4", totalStarted.Load())
	}
}
