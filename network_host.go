// network_host.go — ControlServer adapters that satisfy
// controlserver.NetworkHost.
//
// These adapters expose existing control-server state under the names
// the network bridge will reach for once it moves into
// internal/controlserver. The bridge in package main today still uses
// unexported back-references; the compile-time assertion below pins
// the contract so the later move is a mechanical rename rather than
// an interface negotiation.
package main

import (
	"context"
	"time"

	"github.com/tmc/vz-macos/internal/controlserver"
)

var _ controlserver.NetworkHost = (*ControlServer)(nil)

// LifecycleContext returns the active lifecycle context.
func (s *ControlServer) LifecycleContext() context.Context {
	return s.lifecycleContext()
}

// TimeoutContext derives a context with the given timeout from the
// lifecycle context.
func (s *ControlServer) TimeoutContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	return s.timeoutContext(timeout)
}
