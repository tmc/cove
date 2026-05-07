package main

import (
	"context"
	"testing"
	"time"
)

// TestLifecycleBridgeSetPolicyStartTimeIsSetOnce verifies the set-once
// invariant: the first call records the start time and subsequent
// calls with a different time are ignored. This keeps the max-age
// clock anchored to the first run start across cycle restarts.
func TestLifecycleBridgeSetPolicyStartTimeIsSetOnce(t *testing.T) {
	l := &lifecycleBridge{}

	first := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	l.setPolicyStartTime(first)

	got, _, _ := l.policySnapshot()
	if !got.Equal(first) {
		t.Fatalf("first snapshot startedAt = %v, want %v", got, first)
	}

	second := first.Add(time.Hour)
	l.setPolicyStartTime(second)

	got, _, _ = l.policySnapshot()
	if !got.Equal(first) {
		t.Errorf("startedAt moved on second call: %v, want %v", got, first)
	}
}

// TestLifecycleBridgeNotePolicyExecIncrements verifies the exec
// counter advances by one per call and is visible in the snapshot.
func TestLifecycleBridgeNotePolicyExecIncrements(t *testing.T) {
	l := &lifecycleBridge{}

	if _, n, _ := l.policySnapshot(); n != 0 {
		t.Fatalf("initial execCount = %d, want 0", n)
	}

	l.notePolicyExec()
	l.notePolicyExec()
	l.notePolicyExec()

	if _, n, _ := l.policySnapshot(); n != 3 {
		t.Errorf("execCount = %d, want 3", n)
	}
}

// TestLifecycleBridgeMarkPolicyStopIssuedOneShot verifies the stop
// edge: only the first caller per lifetime returns true, and the
// snapshot reflects the flip thereafter.
func TestLifecycleBridgeMarkPolicyStopIssuedOneShot(t *testing.T) {
	l := &lifecycleBridge{}

	if _, _, issued := l.policySnapshot(); issued {
		t.Fatal("initial stopIssued = true, want false")
	}

	if !l.markPolicyStopIssued() {
		t.Fatal("first markPolicyStopIssued = false, want true")
	}
	if _, _, issued := l.policySnapshot(); !issued {
		t.Error("snapshot stopIssued = false after first mark, want true")
	}

	if l.markPolicyStopIssued() {
		t.Error("second markPolicyStopIssued = true, want false")
	}
	if l.markPolicyStopIssued() {
		t.Error("third markPolicyStopIssued = true, want false")
	}
}

// TestLifecycleBridgeContextBeforeStartReturnsBackground verifies the
// zero-value path: callers that read context() before start()
// receive context.Background() rather than nil, so derived
// timeoutContext calls do not panic.
func TestLifecycleBridgeContextBeforeStartReturnsBackground(t *testing.T) {
	l := &lifecycleBridge{}

	ctx := l.context()
	if ctx == nil {
		t.Fatal("context() = nil, want context.Background()")
	}
	if ctx != context.Background() {
		t.Errorf("context() = %v, want context.Background()", ctx)
	}
	select {
	case <-ctx.Done():
		t.Error("zero-state context is Done, want live")
	default:
	}
}

// TestLifecycleBridgeShutdownCancelsAndIsIdempotent verifies that
// shutdown cancels the context returned by start and that a second
// shutdown call is a no-op.
func TestLifecycleBridgeShutdownCancelsAndIsIdempotent(t *testing.T) {
	l := &lifecycleBridge{}

	ctx := l.start()
	select {
	case <-ctx.Done():
		t.Fatal("start() context already Done")
	default:
	}

	l.shutdown()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel start context within 1s")
	}

	// Second shutdown must not panic and must leave context() at
	// background (not nil).
	l.shutdown()
	if got := l.context(); got != context.Background() {
		t.Errorf("context() after double shutdown = %v, want Background", got)
	}
}

// TestLifecycleBridgeShutdownWithoutStartDoesNotPanic verifies that
// shutdown on a never-started bridge is safe. ControlServer.Stop may
// run before Start in error paths and must not panic.
func TestLifecycleBridgeShutdownWithoutStartDoesNotPanic(t *testing.T) {
	l := &lifecycleBridge{}
	l.shutdown()
	if got := l.context(); got != context.Background() {
		t.Errorf("context() after lone shutdown = %v, want Background", got)
	}
}
