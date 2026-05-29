//go:build !darwin

package main

import (
	"fmt"
	"net"
)

func listenHostVsock(port uint32) (net.Listener, error) {
	return nil, fmt.Errorf("host vsock listen %d: unsupported platform", port)
}
