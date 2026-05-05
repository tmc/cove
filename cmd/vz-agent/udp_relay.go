//go:build darwin || linux

package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"time"
)

type UDPRelay struct {
	vsockPort uint32
	udpAddr   string
	listener  net.Listener
}

func StartUDPRelay(ctx context.Context, vsockPort uint32, udpAddr string) (*UDPRelay, error) {
	lis, err := listenVsock(vsockPort)
	if err != nil {
		return nil, fmt.Errorf("listen vsock %d: %w", vsockPort, err)
	}
	r := &UDPRelay{vsockPort: vsockPort, udpAddr: udpAddr, listener: lis}
	go func() {
		<-ctx.Done()
		r.Close()
	}()
	go r.serve(ctx)
	slog.Info("udp relay started", slog.Int("vsock_port", int(vsockPort)), slog.String("udp_addr", udpAddr))
	return r, nil
}

func (r *UDPRelay) serve(ctx context.Context) {
	for {
		conn, err := r.listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("udp relay: accept", slog.Int("vsock_port", int(r.vsockPort)), slog.Any("err", err))
			return
		}
		go r.relay(conn)
	}
}

func (r *UDPRelay) relay(vsockConn net.Conn) {
	defer vsockConn.Close()
	pkt, err := readUDPFrame(vsockConn)
	if err != nil {
		return
	}
	addr, err := net.ResolveUDPAddr("udp", r.udpAddr)
	if err != nil {
		slog.Error("udp relay: resolve", slog.String("udp_addr", r.udpAddr), slog.Any("err", err))
		return
	}
	udpConn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		slog.Error("udp relay: dial", slog.String("udp_addr", r.udpAddr), slog.Any("err", err))
		return
	}
	defer udpConn.Close()
	if _, err := udpConn.Write(pkt); err != nil {
		return
	}
	buf := make([]byte, 65535)
	udpConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		n, err := udpConn.Read(buf)
		if err != nil {
			return
		}
		if err := writeUDPFrame(vsockConn, buf[:n]); err != nil {
			return
		}
	}
}

func (r *UDPRelay) Close() error {
	return r.listener.Close()
}

type ReverseUDPRelay struct {
	udpPort   int
	vsockPort uint32
	conn      *net.UDPConn
}

func StartReverseUDPRelay(ctx context.Context, udpPort int, vsockPort uint32) (*ReverseUDPRelay, error) {
	addr, err := net.ResolveUDPAddr("udp", net.JoinHostPort("127.0.0.1", strconv.Itoa(udpPort)))
	if err != nil {
		return nil, fmt.Errorf("resolve udp %d: %w", udpPort, err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen udp %d: %w", udpPort, err)
	}
	r := &ReverseUDPRelay{udpPort: udpPort, vsockPort: vsockPort, conn: conn}
	go func() {
		<-ctx.Done()
		r.Close()
	}()
	go r.serve(ctx)
	slog.Info("reverse udp relay started", slog.Int("udp_port", udpPort), slog.Int("vsock_port", int(vsockPort)))
	return r, nil
}

func (r *ReverseUDPRelay) serve(ctx context.Context) {
	buf := make([]byte, 65535)
	for {
		n, addr, err := r.conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("reverse udp relay: read", slog.Int("udp_port", r.udpPort), slog.Any("err", err))
			return
		}
		pkt := append([]byte(nil), buf[:n]...)
		go r.relay(addr, pkt)
	}
}

func (r *ReverseUDPRelay) relay(addr *net.UDPAddr, pkt []byte) {
	vsockConn, err := dialHostVsock(r.vsockPort)
	if err != nil {
		slog.Error("reverse udp relay: dial host vsock", slog.Int("vsock_port", int(r.vsockPort)), slog.Any("err", err))
		return
	}
	defer vsockConn.Close()
	if err := writeUDPFrame(vsockConn, pkt); err != nil {
		return
	}
	vsockConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		reply, err := readUDPFrame(vsockConn)
		if err != nil {
			return
		}
		r.conn.WriteToUDP(reply, addr)
	}
}

func (r *ReverseUDPRelay) Close() error {
	return r.conn.Close()
}

func writeUDPFrame(w io.Writer, pkt []byte) error {
	if len(pkt) > 65535 {
		return fmt.Errorf("udp packet too large: %d", len(pkt))
	}
	var hdr [2]byte
	binary.BigEndian.PutUint16(hdr[:], uint16(len(pkt)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(pkt)
	return err
}

func readUDPFrame(r io.Reader) ([]byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint16(hdr[:])
	pkt := make([]byte, n)
	_, err := io.ReadFull(r, pkt)
	return pkt, err
}
