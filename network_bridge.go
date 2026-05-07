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
	"fmt"
	"net"
	"sync"
	"time"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
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
	listeners.Add(ln)
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

// startITerm2Proxy starts the iTerm2 WebSocket proxy on port. If the
// proxy is already running, returns a success message naming the
// active port without restarting it. Returns nil only if port == 0
// without a default; the caller treats nil as a programming error.
func (n *networkBridge) startITerm2Proxy(port int) *controlpb.ControlResponse {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.iterm2Proxy != nil && n.iterm2Proxy.Running() {
		msg := fmt.Sprintf("iterm2 proxy already running on port %d", n.iterm2Proxy.Port())
		return &controlpb.ControlResponse{Success: true, Data: msg,
			Result: &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: msg}}}
	}

	if port == 0 {
		port = iterm2DefaultPort
	}
	cs := n.cs
	if cs == nil {
		return &controlpb.ControlResponse{Error: "iterm2 proxy: control server not configured"}
	}
	guest := controlServerGuestConnector{vm: cs.vm, queue: cs.vmQueue}
	proxy := newITerm2Proxy(guest, port)
	if err := proxy.Start(); err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("start iterm2 proxy: %v", err)}
	}
	n.iterm2Proxy = proxy

	msg := fmt.Sprintf("iterm2 proxy started on ws://localhost:%d", port)
	return &controlpb.ControlResponse{Success: true, Data: msg,
		Result: &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: msg}}}
}

// iterm2ProxyStopResponse stops a running iTerm2 proxy and returns the
// command response. Distinct from stopITerm2Proxy, which is used by
// the lifecycle Stop path and ignores errors.
func (n *networkBridge) iterm2ProxyStopResponse() *controlpb.ControlResponse {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.iterm2Proxy == nil || !n.iterm2Proxy.Running() {
		return &controlpb.ControlResponse{Success: true, Data: "iterm2 proxy not running",
			Result: &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: "iterm2 proxy not running"}}}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if n.cs != nil {
		ctx, cancel = n.cs.timeoutContext(5 * time.Second)
	}
	defer cancel()
	if err := n.iterm2Proxy.Stop(ctx); err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("stop iterm2 proxy: %v", err)}
	}
	n.iterm2Proxy = nil

	return &controlpb.ControlResponse{Success: true, Data: "iterm2 proxy stopped",
		Result: &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: "iterm2 proxy stopped"}}}
}

// iterm2ProxyStatusResponse returns the current iTerm2 proxy status as
// a control response.
func (n *networkBridge) iterm2ProxyStatusResponse() *controlpb.ControlResponse {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.iterm2Proxy == nil || !n.iterm2Proxy.Running() {
		return &controlpb.ControlResponse{Success: true, Data: "stopped",
			Result: &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: "stopped"}}}
	}

	msg := fmt.Sprintf("running on ws://localhost:%d", n.iterm2Proxy.Port())
	return &controlpb.ControlResponse{Success: true, Data: msg,
		Result: &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: msg}}}
}

// stopITerm2Proxy stops a running iTerm2 proxy, if any. ctx bounds
// the stop. Safe to call multiple times.
func (n *networkBridge) stopITerm2Proxy(ctx context.Context) {
	n.mu.Lock()
	proxy := n.iterm2Proxy
	n.iterm2Proxy = nil
	n.mu.Unlock()
	if proxy != nil {
		proxy.Stop(ctx)
	}
}

// stopPortForwards stops the host-to-guest port-forward manager, if
// any, and detaches it. Safe to call multiple times.
func (n *networkBridge) stopPortForwards() {
	pf := n.clearPortForwardManager()
	if pf != nil {
		pf.StopAll()
	}
}

// closeHTTPListeners closes any HTTP listeners tracked by addHTTPListener
// and clears the list. Safe to call multiple times.
func (n *networkBridge) closeHTTPListeners() {
	n.mu.Lock()
	listeners := n.httpListeners
	n.httpListeners = nil
	n.mu.Unlock()
	if listeners != nil {
		listeners.CloseAll()
	}
}
