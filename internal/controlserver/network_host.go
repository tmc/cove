// NetworkHost is the narrow back-channel the network bridge uses to
// reach state owned by the control server facade in package main.
//
// The network bridge will hold a NetworkHost rather than a
// *ControlServer back-reference so the dependency direction stays
// one-way from package main into this package (per design 039 §7).
package controlserver

import (
	"context"
	"time"
)

// NetworkHost exposes the slice of control-server state the network
// bridge needs: a lifecycle context for long-lived listeners, and a
// timeout context derived from it for bounded shutdown work.
//
// The iTerm2 guest connector — a small value struct that closes over
// the VZ machine handle and dispatch queue — is wired in by the
// bridge move (commit 2) rather than this contract, so package-main
// types stay out of the host interface.
//
// Implementations must be safe for concurrent use.
type NetworkHost interface {
	// LifecycleContext returns the active lifecycle context, or
	// context.Background() if no run is active.
	LifecycleContext() context.Context

	// TimeoutContext derives a context with the given timeout from
	// the lifecycle context.
	TimeoutContext(timeout time.Duration) (context.Context, context.CancelFunc)
}
