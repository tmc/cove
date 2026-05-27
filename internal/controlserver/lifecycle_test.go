package controlserver

import (
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
