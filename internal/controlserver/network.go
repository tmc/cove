// network.go - Network listener sub-component of ControlServer.
//
// NetworkBridge owns the iTerm2 WebSocket proxy, the host-to-guest
// port-forward manager, the HTTP TCP listeners started by StartHTTP,
// and the VNC and debug-stub runtime status records. Holding these in
// one struct narrows the scope of the catch-all ControlServer mutex
// and gives the listener lifecycle a single shutdown path.
package controlserver

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

// NetworkBridge holds the network-listener state owned by a
// ControlServer. The zero value is usable; package main embeds it by
// value and assigns the host back-channel after construction so
// existing &ControlServer{} test constructors keep working.
type NetworkBridge struct {
	host NetworkHost

	mu              sync.Mutex
	iterm2Proxy     *ITerm2Proxy
	portForwards    *PortForwardManager
	httpListeners   *HTTPListeners
	vncStatus       VNCStatus
	debugStubStatus DebugStubStatus
}

// SetHost wires the back-channel used to derive contexts and obtain a
// guest connector. May be called multiple times (e.g. tests).
func (b *NetworkBridge) SetHost(host NetworkHost) { b.host = host }

func (b *NetworkBridge) lifecycleContext() context.Context {
	if b.host == nil {
		return context.Background()
	}
	return b.host.LifecycleContext()
}

func (b *NetworkBridge) timeoutContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	if b.host == nil {
		return context.WithTimeout(context.Background(), timeout)
	}
	return b.host.TimeoutContext(timeout)
}

// PortForwards returns the host-to-guest port-forward manager,
// creating it on first use. Concurrent callers receive the same
// instance.
func (b *NetworkBridge) PortForwards() *PortForwardManager {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.portForwards == nil {
		b.portForwards = NewPortForwardManager(b.lifecycleContext())
	}
	return b.portForwards
}

// ClearPortForwards detaches and returns the current port-forward
// manager, or nil if none is installed. The caller is responsible for
// stopping any active forwards.
func (b *NetworkBridge) ClearPortForwards() *PortForwardManager {
	b.mu.Lock()
	defer b.mu.Unlock()
	pf := b.portForwards
	b.portForwards = nil
	return pf
}

// AddHTTPListener tracks ln so shutdown closes it.
func (b *NetworkBridge) AddHTTPListener(ln net.Listener) {
	b.mu.Lock()
	if b.httpListeners == nil {
		b.httpListeners = &HTTPListeners{}
	}
	listeners := b.httpListeners
	b.mu.Unlock()
	listeners.Add(ln)
}

// SetVNCStatus replaces the recorded VNC runtime status.
func (b *NetworkBridge) SetVNCStatus(status VNCStatus) {
	b.mu.Lock()
	b.vncStatus = status
	b.mu.Unlock()
}

// VNCStatusValue returns the recorded VNC runtime status with a
// derived state field if none was set.
func (b *NetworkBridge) VNCStatusValue() VNCStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	status := b.vncStatus
	if status.State == "" {
		if status.Enabled {
			status.State = "enabled"
		} else {
			status.State = "disabled"
		}
	}
	return status
}

// SetDebugStubStatus replaces the recorded debug-stub runtime status.
func (b *NetworkBridge) SetDebugStubStatus(status DebugStubStatus) {
	b.mu.Lock()
	b.debugStubStatus = status
	b.mu.Unlock()
}

// DebugStubStatusValue returns the recorded debug-stub runtime status
// with a derived state field if none was set.
func (b *NetworkBridge) DebugStubStatusValue() DebugStubStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	status := b.debugStubStatus
	if status.State == "" {
		if status.Enabled {
			status.State = "enabled"
		} else {
			status.State = "disabled"
		}
	}
	return status
}

// StartITerm2Proxy starts the iTerm2 WebSocket proxy on port. If the
// proxy is already running, returns a success message naming the
// active port without restarting it.
func (b *NetworkBridge) StartITerm2Proxy(port int) *controlpb.ControlResponse {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.iterm2Proxy != nil && b.iterm2Proxy.Running() {
		msg := fmt.Sprintf("iterm2 proxy already running on port %d", b.iterm2Proxy.Port())
		return &controlpb.ControlResponse{Success: true, Data: msg,
			Result: &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: msg}}}
	}

	if port == 0 {
		port = ITerm2DefaultPort
	}
	if b.host == nil {
		return &controlpb.ControlResponse{Error: "iterm2 proxy: control server not configured"}
	}
	guest := b.host.GuestConnector()
	proxy := NewITerm2Proxy(guest, port)
	if err := proxy.Start(); err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("start iterm2 proxy: %v", err)}
	}
	b.iterm2Proxy = proxy

	msg := fmt.Sprintf("iterm2 proxy started on ws://localhost:%d", port)
	return &controlpb.ControlResponse{Success: true, Data: msg,
		Result: &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: msg}}}
}

// StopITerm2ProxyResponse stops a running iTerm2 proxy and returns the
// command response. Distinct from StopITerm2Proxy, which is used by
// the lifecycle Stop path and ignores errors.
func (b *NetworkBridge) StopITerm2ProxyResponse() *controlpb.ControlResponse {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.iterm2Proxy == nil || !b.iterm2Proxy.Running() {
		return &controlpb.ControlResponse{Success: true, Data: "iterm2 proxy not running",
			Result: &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: "iterm2 proxy not running"}}}
	}

	ctx, cancel := b.timeoutContext(5 * time.Second)
	defer cancel()
	if err := b.iterm2Proxy.Stop(ctx); err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("stop iterm2 proxy: %v", err)}
	}
	b.iterm2Proxy = nil

	return &controlpb.ControlResponse{Success: true, Data: "iterm2 proxy stopped",
		Result: &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: "iterm2 proxy stopped"}}}
}

// ITerm2ProxyStatusResponse returns the current iTerm2 proxy status as
// a control response.
func (b *NetworkBridge) ITerm2ProxyStatusResponse() *controlpb.ControlResponse {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.iterm2Proxy == nil || !b.iterm2Proxy.Running() {
		return &controlpb.ControlResponse{Success: true, Data: "stopped",
			Result: &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: "stopped"}}}
	}

	msg := fmt.Sprintf("running on ws://localhost:%d", b.iterm2Proxy.Port())
	return &controlpb.ControlResponse{Success: true, Data: msg,
		Result: &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: msg}}}
}

// StopITerm2Proxy stops a running iTerm2 proxy, if any. ctx bounds the
// stop. Safe to call multiple times.
func (b *NetworkBridge) StopITerm2Proxy(ctx context.Context) {
	b.mu.Lock()
	proxy := b.iterm2Proxy
	b.iterm2Proxy = nil
	b.mu.Unlock()
	if proxy != nil {
		proxy.Stop(ctx)
	}
}

// StopPortForwards stops the host-to-guest port-forward manager, if
// any, and detaches it. Safe to call multiple times.
func (b *NetworkBridge) StopPortForwards() {
	pf := b.ClearPortForwards()
	if pf != nil {
		pf.StopAll()
	}
}

// CloseHTTPListeners closes any HTTP listeners tracked by
// AddHTTPListener and clears the list. Safe to call multiple times.
func (b *NetworkBridge) CloseHTTPListeners() {
	b.mu.Lock()
	listeners := b.httpListeners
	b.httpListeners = nil
	b.mu.Unlock()
	if listeners != nil {
		listeners.CloseAll()
	}
}
