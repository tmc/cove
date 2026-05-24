// network_host.go — compile-time assertion that *ControlServer
// satisfies controlserver.NetworkHost, plus the GuestConnector
// adapter that closes over the running VM and dispatch queue.
//
// LifecycleContext and TimeoutContext are defined alongside the
// AgentHost adapter in agent_control.go; they satisfy NetworkHost via
// the same methods.

package main

import (
	"github.com/tmc/cove/internal/controlserver"
)

var _ controlserver.NetworkHost = (*ControlServer)(nil)

// GuestConnector returns a connector that opens vsock connections to
// the running guest. Captured at call time so callers that store the
// returned value see a stable VM/queue pair even if SetVM is called
// later.
func (s *ControlServer) GuestConnector() controlserver.GuestConnector {
	return newControlServerGuestConnector(s)
}

func init() {
	controlserver.ListenHostVsock = listenHostVsock
}
