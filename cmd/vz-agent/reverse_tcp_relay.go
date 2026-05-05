//go:build darwin || linux

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
)

type ReverseTCPRelay struct {
	tcpPort   int
	vsockPort uint32
	listener  net.Listener
}

func StartReverseTCPRelay(ctx context.Context, tcpPort int, vsockPort uint32) (*ReverseTCPRelay, error) {
	lis, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(tcpPort)))
	if err != nil {
		return nil, fmt.Errorf("listen tcp %d: %w", tcpPort, err)
	}
	r := &ReverseTCPRelay{tcpPort: tcpPort, vsockPort: vsockPort, listener: lis}
	go func() {
		<-ctx.Done()
		r.Close()
	}()
	go r.serve(ctx)
	slog.Info("reverse tcp relay started", slog.Int("tcp_port", tcpPort), slog.Int("vsock_port", int(vsockPort)))
	return r, nil
}

func (r *ReverseTCPRelay) serve(ctx context.Context) {
	for {
		conn, err := r.listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("reverse tcp relay: accept", slog.Int("tcp_port", r.tcpPort), slog.Any("err", err))
			return
		}
		go r.relay(conn)
	}
}

func (r *ReverseTCPRelay) relay(tcpConn net.Conn) {
	defer tcpConn.Close()
	vsockConn, err := dialHostVsock(r.vsockPort)
	if err != nil {
		slog.Error("reverse tcp relay: dial host vsock", slog.Int("vsock_port", int(r.vsockPort)), slog.Any("err", err))
		return
	}
	defer vsockConn.Close()
	done := make(chan struct{})
	go func() {
		io.Copy(vsockConn, tcpConn)
		close(done)
	}()
	io.Copy(tcpConn, vsockConn)
	<-done
}

func (r *ReverseTCPRelay) Close() error {
	return r.listener.Close()
}
