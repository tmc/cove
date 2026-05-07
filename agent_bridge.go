// agent_bridge.go - Agent connection and health sub-component of ControlServer.
//
// agentBridge owns the daemon and user agent clients, the connection
// mutex that protects them, and the health-monitor state. It is held
// by ControlServer as a sub-component so agent invariants are local to
// one struct rather than spread across ControlServer fields.
//
// Per design 039 §7 (facade-late rule), the bridge stays in package
// main until all five ControlServer sub-slices have been extracted.
package main

import (
	"sync"

	agentstate "github.com/tmc/vz-macos/internal/agent"
)

// agentBridge holds the agent clients and health state owned by a
// ControlServer. The mu/healthMu pair preserves the prior locking
// shape: mu (formerly ControlServer.agentMu) protects connection
// setup of agent and userAgent; healthMu protects the proactive
// health-monitor record. Splitting them keeps RPC fast paths from
// blocking on health writes.
//
// The zero value is usable; the bridge is embedded in ControlServer
// as a value so existing &ControlServer{...} test constructors
// continue to work without an explicit init step.
type agentBridge struct {
	mu        sync.RWMutex                // protects agent connection setup; RLock for concurrent RPCs
	agent     *agentstate.AgentClient     // GRPC client to guest daemon agent (nil until connected)
	userAgent *agentstate.UserAgentClient // GRPC client to guest user agent (nil until connected)

	healthMu sync.RWMutex
	health   agentHealthState
}
