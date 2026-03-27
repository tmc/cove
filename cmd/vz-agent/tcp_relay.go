package main

import (
	"io"
	"log"
	"net"
	"fmt"
)

// TCPRelay listens on a vsock port and relays connections to a local TCP address.
type TCPRelay struct {
	vsockPort uint32
	tcpAddr   string
	listener  net.Listener
}

// StartTCPRelay creates and starts a relay from vsockPort to tcpAddr.
func StartTCPRelay(vsockPort uint32, tcpAddr string) (*TCPRelay, error) {
	lis, err := listenVsock(vsockPort)
	if err != nil {
		return nil, fmt.Errorf("listen vsock %d: %w", vsockPort, err)
	}
	r := &TCPRelay{
		vsockPort: vsockPort,
		tcpAddr:   tcpAddr,
		listener:  lis,
	}
	go r.serve()
	log.Printf("tcp relay: vsock:%d -> %s", vsockPort, tcpAddr)
	return r, nil
}

func (r *TCPRelay) serve() {
	for {
		conn, err := r.listener.Accept()
		if err != nil {
			log.Printf("tcp relay vsock:%d: accept: %v", r.vsockPort, err)
			return
		}
		go r.relay(conn)
	}
}

func (r *TCPRelay) relay(vsockConn net.Conn) {
	defer vsockConn.Close()

	tcpConn, err := net.Dial("tcp", r.tcpAddr)
	if err != nil {
		log.Printf("tcp relay vsock:%d: dial %s: %v", r.vsockPort, r.tcpAddr, err)
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
