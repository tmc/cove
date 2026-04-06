package main

import (
	"context"
	"io"
	"log"
	"net"
)

const iterm2VsockPort = 1912

// startITerm2Relay listens on vsock port 1912 and relays each connection
// to TCP localhost:1912 (iTerm2 API). This enables host-to-guest iTerm2
// control over vsock without requiring guest TCP to be reachable from host.
func startITerm2Relay(ctx context.Context) {
	lis, err := listenVsock(iterm2VsockPort)
	if err != nil {
		log.Printf("iterm2 relay: listen vsock %d: %v", iterm2VsockPort, err)
		return
	}
	go func() {
		<-ctx.Done()
		lis.Close()
	}()
	log.Printf("iterm2 relay: listening on vsock port %d", iterm2VsockPort)

	for {
		conn, err := lis.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("iterm2 relay: accept: %v", err)
			return
		}
		go relayToITerm2(conn)
	}
}

func relayToITerm2(vsockConn net.Conn) {
	defer vsockConn.Close()

	tcpConn, err := net.Dial("tcp", "localhost:1912")
	if err != nil {
		log.Printf("iterm2 relay: dial localhost:1912: %v", err)
		return
	}
	defer tcpConn.Close()

	done := make(chan struct{})
	go func() {
		io.Copy(tcpConn, vsockConn)
		tcpConn.(*net.TCPConn).CloseWrite()
		close(done)
	}()
	io.Copy(vsockConn, tcpConn)
	<-done
}
