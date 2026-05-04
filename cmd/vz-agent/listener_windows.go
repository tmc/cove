package main

import (
	"fmt"
	"net"
	"strconv"
)

func listenAgent(port uint32, tcpAddr string) (net.Listener, error) {
	if tcpAddr == "" {
		tcpAddr = net.JoinHostPort("", strconv.Itoa(int(port)))
	}
	lis, err := net.Listen("tcp", tcpAddr)
	if err != nil {
		return nil, fmt.Errorf("listen tcp: %w", err)
	}
	return lis, nil
}
