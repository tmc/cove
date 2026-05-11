package controlserver

import (
	"context"
	"testing"
	"time"
)

// TestLifecycleSetPolicyStartTimeIsSetOnce verifies the set-once
// invariant: the first call records the start time and subsequent
// calls with a different time are ignored. This keeps the max-age
// clock anchored to the first run start across cycle restarts.
func TestLifecycleSetPolicyStartTimeIsSetOnce(t *testing.T) {
	l := &Lifecycle{}

	first := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	l.SetPolicyStartTime(first)

	got, _, _ := l.PolicySnapshot()
	if !got.Equal(first) {
		t.Fatalf("first snapshot startedAt = %v, want %v", got, first)
	}

	second := first.Add(time.Hour)
	l.SetPolicyStartTime(second)

	got, _, _ = l.PolicySnapshot()
	if !got.Equal(first) {
		t.Errorf("startedAt moved on second call: %v, want %v", got, first)
	}
}

// TestLifecycleNotePolicyExecIncrements verifies the exec counter
// advances by one per call and is visible in the snapshot.
func TestLifecycleNotePolicyExecIncrements(t *testing.T) {
	l := &Lifecycle{}

	if _, n, _ := l.PolicySnapshot(); n != 0 {
		t.Fatalf("initial execCount = %d, want 0", n)
	}

	l.NotePolicyExec()
	l.NotePolicyExec()
	l.NotePolicyExec()

	if _, n, _ := l.PolicySnapshot(); n != 3 {
		t.Errorf("execCount = %d, want 3", n)
	}
}

// TestLifecycleMarkPolicyStopIssuedOneShot verifies the stop edge:
// only the first caller per lifetime returns true, and the snapshot
// reflects the flip thereafter.
func TestLifecycleMarkPolicyStopIssuedOneShot(t *testing.T) {
	l := &Lifecycle{}

	if _, _, issued := l.PolicySnapshot(); issued {
		t.Fatal("initial stopIssued = true, want false")
	}

	if !l.MarkPolicyStopIssued() {
		t.Fatal("first MarkPolicyStopIssued = false, want true")
	}
	if _, _, issued := l.PolicySnapshot(); !issued {
		t.Error("snapshot stopIssued = false after first mark, want true")
	}

	if l.MarkPolicyStopIssued() {
		t.Error("second MarkPolicyStopIssued = true, want false")
	}
	if l.MarkPolicyStopIssued() {
		t.Error("third MarkPolicyStopIssued = true, want false")
	}
}

// TestLifecycleContextBeforeStartReturnsBackground verifies the
// zero-value path: callers that read Context() before Start() receive
// context.Background() rather than nil, so derived TimeoutContext
// calls do not panic.
func TestLifecycleContextBeforeStartReturnsBackground(t *testing.T) {
	l := &Lifecycle{}

	ctx := l.Context()
	if ctx == nil {
		t.Fatal("Context() = nil, want context.Background()")
	}
	if ctx != context.Background() {
		t.Errorf("Context() = %v, want context.Background()", ctx)
	}
	select {
	case <-ctx.Done():
		t.Error("zero-state context is Done, want live")
	default:
	}
}

// TestLifecycleShutdownCancelsAndIsIdempotent verifies that Shutdown
// cancels the context returned by Start and that a second Shutdown
// call is a no-op.
func TestLifecycleShutdownCancelsAndIsIdempotent(t *testing.T) {
	l := &Lifecycle{}

	ctx := l.Start()
	select {
	case <-ctx.Done():
		t.Fatal("Start() context already Done")
	default:
	}

	l.Shutdown()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not cancel start context within 1s")
	}

	l.Shutdown()
	if got := l.Context(); got != context.Background() {
		t.Errorf("Context() after double Shutdown = %v, want Background", got)
	}
}

// TestLifecycleShutdownWithoutStartDoesNotPanic verifies that
// Shutdown on a never-started Lifecycle is safe. ControlServer.Stop
// may run before Start in error paths and must not panic.
func TestLifecycleShutdownWithoutStartDoesNotPanic(t *testing.T) {
	l := &Lifecycle{}
	l.Shutdown()
	if got := l.Context(); got != context.Background() {
		t.Errorf("Context() after lone Shutdown = %v, want Background", got)
	}
}

func TestLifecycleTimeoutContext(t *testing.T) {
	_ = t.TempDir()
	tests := []struct {
		name     string
		start    bool
		shutdown bool
	}{
		{"background", false, false},
		{"started", true, false},
		{"shutdown", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var l Lifecycle
			if tt.start {
				l.Start()
			}
			ctx, cancel := l.TimeoutContext(time.Hour)
			defer cancel()
			if tt.shutdown {
				l.Shutdown()
			}
			select {
			case <-ctx.Done():
				if !tt.shutdown {
					t.Fatal("timeout context canceled early")
				}
			default:
				if tt.shutdown {
					t.Fatal("timeout context still live after shutdown")
				}
			}
		})
	}
}
