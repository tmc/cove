package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
)

// httpListeners tracks TCP listeners started by StartHTTP so Stop() can close them.
type httpListeners struct {
	mu  sync.Mutex
	lns []net.Listener
}

func (h *httpListeners) add(ln net.Listener) {
	h.mu.Lock()
	h.lns = append(h.lns, ln)
	h.mu.Unlock()
}

func (h *httpListeners) closeAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ln := range h.lns {
		ln.Close()
	}
	h.lns = nil
}

// StartHTTP binds a TCP listener on addr and serves the per-VM HTTP API.
// vmName is used in URL path matching (e.g. /v1/vms/<vmName>/status).
// The listener is tracked so ControlServer.Stop closes it.
func (s *ControlServer) StartHTTP(addr, vmName string) (net.Listener, error) {
	handler := NewHTTPHandler(s, vmName, s.authToken, nil)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("http listen %s: %w", addr, err)
	}
	if s.httpListeners == nil {
		s.httpListeners = &httpListeners{}
	}
	s.httpListeners.add(ln)
	go func() {
		if err := http.Serve(ln, handler); err != nil && !isClosedError(err) {
			fmt.Fprintf(os.Stderr, "cove: http server: %v\n", err)
		}
	}()
	return ln, nil
}
