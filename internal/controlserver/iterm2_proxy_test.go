package controlserver

import (
	"context"
	"errors"
	"net"
	"net/http"
	"syscall"
	"testing"
)

func TestITerm2ProxyDefaults(t *testing.T) {
	p := NewITerm2Proxy(nil, 0)
	if got := p.Port(); got != ITerm2DefaultPort {
		t.Fatalf("Port = %d, want %d", got, ITerm2DefaultPort)
	}
	if p.Guest() != nil {
		t.Fatal("Guest = non-nil, want nil")
	}
	if p.Running() {
		t.Fatal("Running = true, want false")
	}
}

func TestAllowLocalhostOrigin(t *testing.T) {
	tests := []struct {
		name   string
		origin string
		want   bool
	}{
		{"empty", "", true},
		{"localhost", "http://localhost:1913", true},
		{"ipv4", "http://127.0.0.1:1913", true},
		{"ipv6", "http://[::1]:1913", true},
		{"remote", "http://example.com", false},
		{"bad", "://bad", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &http.Request{Header: make(http.Header)}
			r.Header.Set("Origin", tt.origin)
			if got := allowLocalhostOrigin(r); got != tt.want {
				t.Fatalf("allowLocalhostOrigin(%q) = %v, want %v", tt.origin, got, tt.want)
			}
		})
	}
}

func TestITerm2ProxyStartStop(t *testing.T) {
	_ = t.TempDir()
	p := NewITerm2Proxy(nil, 0)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	p.port = ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !p.Running() {
		t.Fatal("Running = false, want true")
	}
	if err := p.Start(); err == nil {
		t.Fatal("second Start succeeded, want error")
	}
	if err := p.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if p.Running() {
		t.Fatal("Running = true, want false")
	}
}

func TestITerm2ProxyStartListenError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer ln.Close()

	p := NewITerm2Proxy(nil, ln.Addr().(*net.TCPAddr).Port)
	if err := p.Start(); !errors.Is(err, syscall.EADDRINUSE) {
		t.Fatalf("Start err = %v, want address in use", err)
	}
}
