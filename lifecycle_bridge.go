// lifecycle_bridge.go - Lifecycle and policy state owned by ControlServer.
//
// lifecycleBridge holds the cancellable lifecycle context and the
// per-VM policy counters (start time, exec count, stop edge) that
// drive the runtime policy monitor in runtime_lifecycle.go. Splitting
// these out of ControlServer keeps the lifecycle invariants — context
// lifetime, set-once start time, one-shot stop edge — local to one
// struct and reduces the mutex count on the parent.
//
// Per design 039 §7 (facade-late rule), the bridge stays in package
// main until all five ControlServer sub-slices have been extracted.
package main

import (
	"context"
	"sync"
	"time"
)

// lifecycleBridge owns the cancellable lifecycle context and the
// policy-monitor counters. The mu/policyMu pair preserves the prior
// locking shape: mu (formerly ControlServer.lifecycleMu) protects
// ctx/cancel; policyMu protects the four policy fields. Splitting them
// keeps fast-path reads of the lifecycle context off the policy lock.
//
// The zero value is usable; the bridge is embedded in ControlServer as
// a value so existing &ControlServer{...} test constructors continue
// to work without an explicit init step.
type lifecycleBridge struct {
	mu     sync.RWMutex
	ctx    context.Context
	cancel context.CancelFunc

	policyMu         sync.Mutex
	policyStartedAt  time.Time
	policyExecCount  int64
	policyStopIssued bool
}

// start initializes the lifecycle context if it is not already live.
// Returns the active context.
func (l *lifecycleBridge) start() context.Context {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ctx == nil || l.cancel == nil {
		l.ctx, l.cancel = context.WithCancel(context.Background())
	}
	return l.ctx
}

// shutdown cancels the lifecycle context and clears it. Safe to call
// multiple times; subsequent calls are no-ops.
func (l *lifecycleBridge) shutdown() {
	l.mu.Lock()
	cancel := l.cancel
	l.cancel = nil
	l.ctx = nil
	l.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// context returns the active lifecycle context, or context.Background
// if shutdown has run or start was never called.
func (l *lifecycleBridge) context() context.Context {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.ctx == nil {
		return context.Background()
	}
	return l.ctx
}

// timeoutContext returns a context derived from the lifecycle context
// with the given timeout.
func (l *lifecycleBridge) timeoutContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(l.context(), timeout)
}

// setPolicyStartTime records the VM run-start time. The first call
// wins; later calls are no-ops so cycle restarts don't reset the
// max-age clock.
func (l *lifecycleBridge) setPolicyStartTime(now time.Time) {
	l.policyMu.Lock()
	if l.policyStartedAt.IsZero() {
		l.policyStartedAt = now
	}
	l.policyMu.Unlock()
}

// policySnapshot returns the current policy counters atomically.
func (l *lifecycleBridge) policySnapshot() (time.Time, int64, bool) {
	l.policyMu.Lock()
	defer l.policyMu.Unlock()
	return l.policyStartedAt, l.policyExecCount, l.policyStopIssued
}

// notePolicyExec increments the per-run exec counter that the policy
// monitor reports as runs_used.
func (l *lifecycleBridge) notePolicyExec() {
	l.policyMu.Lock()
	l.policyExecCount++
	l.policyMu.Unlock()
}

// markPolicyStopIssued atomically transitions the stop edge from
// false to true and reports whether it was the caller that flipped
// it. Only the first caller per lifetime returns true.
func (l *lifecycleBridge) markPolicyStopIssued() bool {
	l.policyMu.Lock()
	defer l.policyMu.Unlock()
	if l.policyStopIssued {
		return false
	}
	l.policyStopIssued = true
	return true
}
