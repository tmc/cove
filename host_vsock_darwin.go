//go:build darwin

package main

import (
	"fmt"
	"net"
	"os"
	"syscall"
	"unsafe"
)

func listenHostVsock(port uint32) (net.Listener, error) {
	const (
		afVsock = 40
	)
	fd, err := syscall.Socket(afVsock, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("socket: %w", err)
	}
	sa := [12]byte{}
	sa[0] = 12
	sa[1] = afVsock
	sa[4] = byte(port)
	sa[5] = byte(port >> 8)
	sa[6] = byte(port >> 16)
	sa[7] = byte(port >> 24)
	sa[8] = 0xff
	sa[9] = 0xff
	sa[10] = 0xff
	sa[11] = 0xff
	if _, _, errno := syscall.RawSyscall(syscall.SYS_BIND, uintptr(fd), uintptr(unsafe.Pointer(&sa[0])), uintptr(len(sa))); errno != 0 {
		syscall.Close(fd)
		return nil, fmt.Errorf("bind: %w", errno)
	}
	if _, _, errno := syscall.RawSyscall(syscall.SYS_LISTEN, uintptr(fd), 128, 0); errno != 0 {
		syscall.Close(fd)
		return nil, fmt.Errorf("listen: %w", errno)
	}
	return &hostVsockListener{file: os.NewFile(uintptr(fd), "host-vsock")}, nil
}
