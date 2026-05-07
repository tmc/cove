// Lifecycle holds the cancellable lifecycle context and the per-VM
// policy counters that drive the runtime policy monitor.
package controlserver

import (
	"context"
	"sync"
	"time"
)

// Lifecycle owns the cancellable lifecycle context and the
// policy-monitor counters. The mu/policyMu pair preserves the
// pre-extraction locking shape: mu protects ctx/cancel; policyMu
// protects the four policy fields. Splitting them keeps fast-path
// reads of the lifecycle context off the policy lock.
//
// The zero value is usable.
type Lifecycle struct {
	mu     sync.RWMutex
	ctx    context.Context
	cancel context.CancelFunc

	policyMu         sync.Mutex
	policyStartedAt  time.Time
	policyExecCount  int64
	policyStopIssued bool
}

// Start initializes the lifecycle context if it is not already live.
// Returns the active context.
func (l *Lifecycle) Start() context.Context {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ctx == nil || l.cancel == nil {
		l.ctx, l.cancel = context.WithCancel(context.Background())
	}
	return l.ctx
}

// Shutdown cancels the lifecycle context and clears it. Safe to call
// multiple times; subsequent calls are no-ops.
func (l *Lifecycle) Shutdown() {
	l.mu.Lock()
	cancel := l.cancel
	l.cancel = nil
	l.ctx = nil
	l.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Context returns the active lifecycle context, or context.Background
// if Shutdown has run or Start was never called.
func (l *Lifecycle) Context() context.Context {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.ctx == nil {
		return context.Background()
	}
	return l.ctx
}

// TimeoutContext returns a context derived from the lifecycle context
// with the given timeout.
func (l *Lifecycle) TimeoutContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(l.Context(), timeout)
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
