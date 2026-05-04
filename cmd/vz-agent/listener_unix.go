//go:build darwin || linux

package main

import (
	"fmt"
	"net"
)

func listenAgent(port uint32, tcpAddr string) (net.Listener, error) {
	if tcpAddr != "" {
		lis, err := net.Listen("tcp", tcpAddr)
		if err != nil {
			return nil, fmt.Errorf("listen tcp: %w", err)
		}
		return lis, nil
	}
	return listenVsock(port)
}
