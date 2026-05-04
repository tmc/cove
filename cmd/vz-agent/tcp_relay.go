//go:build darwin || linux

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
)

// TCPRelay listens on a vsock port and relays connections to a local TCP address.
type TCPRelay struct {
	vsockPort uint32
	tcpAddr   string
	listener  net.Listener
}

// StartTCPRelay creates and starts a relay from vsockPort to tcpAddr.
func StartTCPRelay(ctx context.Context, vsockPort uint32, tcpAddr string) (*TCPRelay, error) {
	lis, err := listenVsock(vsockPort)
	if err != nil {
		return nil, fmt.Errorf("listen vsock %d: %w", vsockPort, err)
	}
	r := &TCPRelay{
		vsockPort: vsockPort,
		tcpAddr:   tcpAddr,
		listener:  lis,
	}
	go func() {
		<-ctx.Done()
		r.Close()
	}()
	go r.serve(ctx)
	slog.Info("tcp relay started",
		slog.Int("vsock_port", int(vsockPort)),
		slog.String("tcp_addr", tcpAddr))
	return r, nil
}

func (r *TCPRelay) serve(ctx context.Context) {
	for {
		conn, err := r.listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("tcp relay: accept",
				slog.Int("vsock_port", int(r.vsockPort)),
				slog.Any("err", err))
			return
		}
		go r.relay(conn)
	}
}

func (r *TCPRelay) relay(vsockConn net.Conn) {
	defer vsockConn.Close()

	tcpConn, err := net.Dial("tcp", r.tcpAddr)
	if err != nil {
		slog.Error("tcp relay: dial",
			slog.Int("vsock_port", int(r.vsockPort)),
			slog.String("tcp_addr", r.tcpAddr),
			slog.Any("err", err))
		return
	}
	defer tcpConn.Close()

	done := make(chan struct{})
	go func() {
		io.Copy(tcpConn, vsockConn)
		if tc, ok := tcpConn.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
		close(done)
	}()
	io.Copy(vsockConn, tcpConn)
	<-done
}

// Close stops the relay listener.
func (r *TCPRelay) Close() error {
	return r.listener.Close()
}
