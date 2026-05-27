package controlserver

import (
	"sync"
	"time"
)

// Lifecycle owns the policy-monitor counters. The zero value is usable.
type Lifecycle struct {
	policyMu         sync.Mutex
	policyStartedAt  time.Time
	policyExecCount  int64
	policyStopIssued bool
}

// SetPolicyStartTime records the VM run-start time. The first call
// wins; later calls are no-ops so cycle restarts don't reset the
// max-age clock.
func (l *Lifecycle) SetPolicyStartTime(now time.Time) {
	l.policyMu.Lock()
	if l.policyStartedAt.IsZero() {
		l.policyStartedAt = now
	}
	l.policyMu.Unlock()
}

// PolicySnapshot returns the current policy counters atomically.
func (l *Lifecycle) PolicySnapshot() (time.Time, int64, bool) {
	l.policyMu.Lock()
	defer l.policyMu.Unlock()
	return l.policyStartedAt, l.policyExecCount, l.policyStopIssued
}

// NotePolicyExec increments the per-run exec counter that the policy
// monitor reports as runs_used.
func (l *Lifecycle) NotePolicyExec() {
	l.policyMu.Lock()
	l.policyExecCount++
	l.policyMu.Unlock()
}

// MarkPolicyStopIssued atomically transitions the stop edge from
// false to true and reports whether it was the caller that flipped
// it. Only the first caller per lifetime returns true.
func (l *Lifecycle) MarkPolicyStopIssued() bool {
	l.policyMu.Lock()
	defer l.policyMu.Unlock()
	if l.policyStopIssued {
		return false
	}
	l.policyStopIssued = true
	return true
}
