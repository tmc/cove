// HTTPListeners tracks TCP listeners started by ControlServer.StartHTTP
// so Stop() can close them. Held by the network bridge.
package controlserver

import (
	"net"
	"sync"
)

// HTTPListeners is a thread-safe collection of net.Listener handles.
// The zero value is usable.
type HTTPListeners struct {
	mu  sync.Mutex
	lns []net.Listener
}

// Add records ln so a later CloseAll closes it.
func (h *HTTPListeners) Add(ln net.Listener) {
	h.mu.Lock()
	h.lns = append(h.lns, ln)
	h.mu.Unlock()
}

// CloseAll closes every recorded listener and clears the list.
func (h *HTTPListeners) CloseAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ln := range h.lns {
		ln.Close()
	}
	h.lns = nil
}
