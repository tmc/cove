// network_host.go — compile-time assertion that *ControlServer
// satisfies controlserver.NetworkHost.
//
// LifecycleContext and TimeoutContext are defined alongside the
// AgentHost adapter in agent_control.go; they satisfy NetworkHost via
// the same methods.
package main

import (
	"github.com/tmc/vz-macos/internal/controlserver"
)

var _ controlserver.NetworkHost = (*ControlServer)(nil)
