// network_bridge.go - Network listener sub-component of ControlServer.
//
// networkBridge owns the iTerm2 WebSocket proxy, the host-to-guest
// port-forward manager, the HTTP TCP listeners started by StartHTTP,
// and the VNC and debug-stub runtime status records. Holding these in
// one struct narrows the scope of the catch-all ControlServer mutex
// and gives the listener lifecycle a single shutdown path.
//
// Per design 039 §7 (facade-late rule), the bridge stays in package
// main until all five ControlServer sub-slices have been extracted.
package main

import (
	"context"
	"net"
	"sync"
)

// networkBridge holds the network-listener state owned by a
// ControlServer. The zero value is usable; the bridge is embedded in
// ControlServer as a value so existing &ControlServer{...} test
// constructors continue to work without an explicit init step.
type networkBridge struct {
	cs *ControlServer // back-reference for VM access and lifecycle context

	mu              sync.Mutex
	iterm2Proxy     *ITerm2Proxy
	portForwards    *PortForwardManager
	httpListeners   *httpListeners
	vncStatus       VNCStatus
	debugStubStatus DebugStubStatus
}

// portForwardManager returns the host-to-guest port-forward manager,
// creating it on first use. Concurrent callers receive the same
// instance.
func (n *networkBridge) portForwardManager() *PortForwardManager {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.portForwards == nil {
		ctx := context.Background()
		if n.cs != nil {
			ctx = n.cs.lifecycleContext()
		}
		n.portForwards = NewPortForwardManager(ctx)
	}
	return n.portForwards
}

// clearPortForwardManager detaches and returns the current port-forward
// manager, or nil if none is installed. The caller is responsible for
// stopping any active forwards.
func (n *networkBridge) clearPortForwardManager() *PortForwardManager {
	n.mu.Lock()
	defer n.mu.Unlock()
	pf := n.portForwards
	n.portForwards = nil
	return pf
}

// addHTTPListener tracks ln so shutdown closes it.
func (n *networkBridge) addHTTPListener(ln net.Listener) {
	n.mu.Lock()
	if n.httpListeners == nil {
		n.httpListeners = &httpListeners{}
	}
	listeners := n.httpListeners
	n.mu.Unlock()
	listeners.add(ln)
}

// setVNCStatus replaces the recorded VNC runtime status.
func (n *networkBridge) setVNCStatus(status VNCStatus) {
	n.mu.Lock()
	n.vncStatus = status
	n.mu.Unlock()
}

// vncStatusValue returns the recorded VNC runtime status with a
// derived state field if none was set.
func (n *networkBridge) vncStatusValue() VNCStatus {
	n.mu.Lock()
	defer n.mu.Unlock()
	status := n.vncStatus
	if status.State == "" {
		if status.Enabled {
			status.State = "enabled"
		} else {
			status.State = "disabled"
		}
	}
	return status
}

// setDebugStubStatus replaces the recorded debug-stub runtime status.
func (n *networkBridge) setDebugStubStatus(status DebugStubStatus) {
	n.mu.Lock()
	n.debugStubStatus = status
	n.mu.Unlock()
}

// debugStubStatusValue returns the recorded debug-stub runtime status
// with a derived state field if none was set.
func (n *networkBridge) debugStubStatusValue() DebugStubStatus {
	n.mu.Lock()
	defer n.mu.Unlock()
	status := n.debugStubStatus
	if status.State == "" {
		if status.Enabled {
			status.State = "enabled"
		} else {
			status.State = "disabled"
		}
	}
	return status
}

// shutdown closes the iTerm2 proxy, port-forward manager, and HTTP
// listeners. ctx bounds the iTerm2 proxy stop. shutdown is safe to
// call multiple times.
func (n *networkBridge) shutdown(ctx context.Context) {
	n.mu.Lock()
	proxy := n.iterm2Proxy
	n.iterm2Proxy = nil
	pf := n.portForwards
	n.portForwards = nil
	listeners := n.httpListeners
	n.httpListeners = nil
	n.mu.Unlock()

	if proxy != nil {
		proxy.Stop(ctx)
	}
	if pf != nil {
		pf.StopAll()
	}
	if listeners != nil {
		listeners.closeAll()
	}
}
