// NetworkHost is the narrow back-channel the network bridge uses to
// reach state owned by the control server facade in package main.
//
// The network bridge holds a NetworkHost rather than a *ControlServer
// back-reference so the dependency direction stays one-way from
// package main into this package (per design 039 §7).
package controlserver

import (
	"context"
	"net"
	"time"
)

// GuestConnector opens a connection to a guest vsock port. The
// iTerm2 proxy and the port-forward manager use it to relay traffic
// from a host listener into the running guest.
type GuestConnector interface {
	ConnectToGuestPort(port uint32) (net.Conn, error)
}

// NetworkHost exposes the slice of control-server state the network
// bridge needs: a lifecycle context for long-lived listeners, a
// timeout context derived from it for bounded shutdown work, and a
// guest connector for iTerm2-proxy and port-forward construction.
//
// Implementations must be safe for concurrent use.
type NetworkHost interface {
	// LifecycleContext returns the active lifecycle context, or
	// context.Background() if no run is active.
	LifecycleContext() context.Context

	// TimeoutContext derives a context with the given timeout from
	// the lifecycle context.
	TimeoutContext(timeout time.Duration) (context.Context, context.CancelFunc)

	// GuestConnector returns a connector for the running guest. May
	// return a connector that fails to dial if no VM is configured;
	// the caller surfaces the error.
	GuestConnector() GuestConnector
}
