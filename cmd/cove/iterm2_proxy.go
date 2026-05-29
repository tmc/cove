// iterm2_proxy.go - Package-main glue for the iTerm2 WebSocket proxy.
//
// The proxy implementation lives in internal/controlserver. This file
// keeps the *ControlServer-bound entry points used by the control
// command dispatcher and the legacy NewITerm2Proxy(cs) constructor.

package main

import (
	"encoding/json"

	"github.com/tmc/cove/internal/controlserver"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

// ITerm2Proxy is an alias of controlserver.ITerm2Proxy. The type lives
// in internal/controlserver so the network bridge can hold it without
// crossing the package-main boundary.
type ITerm2Proxy = controlserver.ITerm2Proxy

const (
	iterm2VsockPort     = controlserver.ITerm2VsockPort
	iterm2DefaultPort   = controlserver.ITerm2DefaultPort
	iterm2WSSubprotocol = controlserver.ITerm2WSSubprotocol
)

// NewITerm2Proxy creates a proxy bound to the given ControlServer. The
// guest connector is captured at construction time so later VM swaps
// on s do not affect this proxy instance.
func NewITerm2Proxy(cs *ControlServer, port int) *controlserver.ITerm2Proxy {
	return controlserver.NewITerm2Proxy(newControlServerGuestConnector(cs), port)
}

// parseITerm2ProxyPort extracts an optional port from the raw JSON request.
// Returns 0 if no port is specified.
func parseITerm2ProxyPort(rawJSON []byte) int {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rawJSON, &raw); err != nil {
		return 0
	}
	blob, ok := raw["data"]
	if !ok {
		return 0
	}
	var data struct {
		Port int `json:"port"`
	}
	if err := json.Unmarshal(blob, &data); err != nil {
		return 0
	}
	return data.Port
}

// handleITerm2ProxyStart starts the iTerm2 WebSocket proxy on the default port.
func (s *ControlServer) handleITerm2ProxyStart() *controlpb.ControlResponse {
	return s.handleITerm2ProxyStartWithPort(iterm2DefaultPort)
}

// handleITerm2ProxyStartWithPort starts the proxy on a specific port.
func (s *ControlServer) handleITerm2ProxyStartWithPort(port int) *controlpb.ControlResponse {
	return s.network.StartITerm2Proxy(port)
}

// handleITerm2ProxyStop stops the iTerm2 WebSocket proxy.
func (s *ControlServer) handleITerm2ProxyStop() *controlpb.ControlResponse {
	return s.network.StopITerm2ProxyResponse()
}

// handleITerm2ProxyStatus returns the current proxy status.
func (s *ControlServer) handleITerm2ProxyStatus() *controlpb.ControlResponse {
	return s.network.ITerm2ProxyStatusResponse()
}
