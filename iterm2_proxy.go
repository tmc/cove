// iterm2_proxy.go - WebSocket-to-vsock relay for iTerm2 API access.
//
// Bridges host-side WebSocket clients to the guest's iTerm2 WebSocket API
// running on vsock port 1912. Each incoming WebSocket connection opens a
// new vsock connection to the guest and relays binary frames bidirectionally.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"

	"github.com/gorilla/websocket"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

const (
	iterm2VsockPort     = 1912
	iterm2DefaultPort   = 1913
	iterm2WSSubprotocol = "api.iterm2.com"
)

// ITerm2Proxy relays WebSocket connections to the guest iTerm2 API via vsock.
type ITerm2Proxy struct {
	port     int
	server   *http.Server
	guest    controlServerGuestConnector
	mu       sync.Mutex
	running  bool
	upgrader websocket.Upgrader
}

// NewITerm2Proxy creates a proxy bound to the given ControlServer.
func NewITerm2Proxy(cs *ControlServer, port int) *ITerm2Proxy {
	return newITerm2Proxy(newControlServerGuestConnector(cs), port)
}

func newITerm2Proxy(guest controlServerGuestConnector, port int) *ITerm2Proxy {
	if port == 0 {
		port = iterm2DefaultPort
	}
	return &ITerm2Proxy{
		port:  port,
		guest: guest,
		upgrader: websocket.Upgrader{
			Subprotocols:    []string{iterm2WSSubprotocol},
			CheckOrigin:     allowLocalhostOrigin,
			ReadBufferSize:  64 * 1024,
			WriteBufferSize: 64 * 1024,
		},
	}
}

func allowLocalhostOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

// Start begins listening for WebSocket connections.
func (p *ITerm2Proxy) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.running {
		return fmt.Errorf("iterm2 proxy already running on port %d", p.port)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", p.handleWS)

	addr := fmt.Sprintf("localhost:%d", p.port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}

	p.server = &http.Server{Handler: mux}
	p.running = true

	go func() {
		if err := p.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			fmt.Printf("[iterm2-proxy] server error: %v\n", err)
		}
		p.mu.Lock()
		p.running = false
		p.mu.Unlock()
	}()

	fmt.Printf("[iterm2-proxy] listening on ws://%s\n", addr)
	return nil
}

// Stop shuts down the proxy server.
func (p *ITerm2Proxy) Stop(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.running {
		return nil
	}
	err := p.server.Shutdown(ctx)
	p.running = false
	return err
}

// Running reports whether the proxy is currently listening.
func (p *ITerm2Proxy) Running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running
}

// Port returns the configured listen port.
func (p *ITerm2Proxy) Port() int {
	return p.port
}

func (p *ITerm2Proxy) handleWS(w http.ResponseWriter, r *http.Request) {
	wsConn, err := p.upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Printf("[iterm2-proxy] upgrade: %v\n", err)
		return
	}
	defer wsConn.Close()

	vsockConn, err := p.dialGuest()
	if err != nil {
		fmt.Printf("[iterm2-proxy] vsock dial: %v\n", err)
		wsConn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "vsock dial failed"))
		return
	}
	defer vsockConn.Close()

	if verbose {
		fmt.Printf("[iterm2-proxy] new session: ws client %s -> vsock:%d\n",
			r.RemoteAddr, iterm2VsockPort)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// WebSocket -> vsock
	go func() {
		defer wg.Done()
		defer vsockConn.Close()
		for {
			_, msg, err := wsConn.ReadMessage()
			if err != nil {
				if verbose && !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					fmt.Printf("[iterm2-proxy] ws read: %v\n", err)
				}
				return
			}
			if _, err := vsockConn.Write(msg); err != nil {
				if verbose {
					fmt.Printf("[iterm2-proxy] vsock write: %v\n", err)
				}
				return
			}
		}
	}()

	// vsock -> WebSocket
	go func() {
		defer wg.Done()
		defer wsConn.Close()
		buf := make([]byte, 64*1024)
		for {
			n, err := vsockConn.Read(buf)
			if n > 0 {
				if writeErr := wsConn.WriteMessage(websocket.BinaryMessage, buf[:n]); writeErr != nil {
					if verbose {
						fmt.Printf("[iterm2-proxy] ws write: %v\n", writeErr)
					}
					return
				}
			}
			if err != nil {
				if err != io.EOF && verbose {
					fmt.Printf("[iterm2-proxy] vsock read: %v\n", err)
				}
				return
			}
		}
	}()

	wg.Wait()
	if verbose {
		fmt.Printf("[iterm2-proxy] session closed: %s\n", r.RemoteAddr)
	}
}

func (p *ITerm2Proxy) dialGuest() (net.Conn, error) {
	conn, err := p.guest.ConnectToGuestPort(iterm2VsockPort)
	if err != nil {
		return nil, fmt.Errorf("connect port %d: %w", iterm2VsockPort, err)
	}
	return conn, nil
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
	return s.network.startITerm2Proxy(port)
}

// handleITerm2ProxyStop stops the iTerm2 WebSocket proxy.
func (s *ControlServer) handleITerm2ProxyStop() *controlpb.ControlResponse {
	return s.network.iterm2ProxyStopResponse()
}

// handleITerm2ProxyStatus returns the current proxy status.
func (s *ControlServer) handleITerm2ProxyStatus() *controlpb.ControlResponse {
	return s.network.iterm2ProxyStatusResponse()
}
