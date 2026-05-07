package main

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"

	"github.com/tmc/vz-macos/internal/controlserver"
)

// httpListeners is an alias of controlserver.HTTPListeners. The type
// lives in internal/controlserver so the network bridge (extracted
// next) can hold it without crossing the package-main boundary.
type httpListeners = controlserver.HTTPListeners

// StartHTTP binds a TCP listener on addr and serves the per-VM HTTP API.
// vmName is used in URL path matching (e.g. /v1/vms/<vmName>/status).
// The listener is tracked so ControlServer.Stop closes it.
func (s *ControlServer) StartHTTP(addr, vmName string) (net.Listener, error) {
	handler := NewHTTPHandler(s, vmName, s.authToken, nil)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("http listen %s: %w", addr, err)
	}
	s.network.addHTTPListener(ln)
	go func() {
		if err := http.Serve(ln, handler); err != nil && !isClosedError(err) {
			slog.Error("http server",
				slog.String("vm", vmName),
				slog.Any("err", err))
		}
	}()
	return ln, nil
}
