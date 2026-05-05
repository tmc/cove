package main

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"syscall"
)

// listenVsock creates a vsock listener on the given port.
// Linux: AF_VSOCK = 40, sockaddr_vm has no svm_len (16 bytes total).
func listenVsock(port uint32) (net.Listener, error) {
	const (
		AF_VSOCK       = 40
		VMADDR_CID_ANY = 0xFFFFFFFF
	)

	fd, err := syscall.Socket(AF_VSOCK, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("socket: %w", err)
	}

	// sockaddr_vm layout for Linux:
	//   uint16 svm_family   (2 bytes)
	//   uint16 svm_reserved (2 bytes)
	//   uint32 svm_port     (4 bytes)
	//   uint32 svm_cid      (4 bytes)
	//   uint8  svm_zero[4]  (4 bytes)
	sa := [16]byte{}
	binary.LittleEndian.PutUint16(sa[0:2], AF_VSOCK)
	// svm_reserved = 0
	binary.LittleEndian.PutUint32(sa[4:8], port)
	binary.LittleEndian.PutUint32(sa[8:12], VMADDR_CID_ANY)
	// svm_zero = 0

	_, _, errno := syscall.RawSyscall(
		syscall.SYS_BIND,
		uintptr(fd),
		uintptr(unsafePointer(&sa[0])),
		16,
	)
	if errno != 0 {
		syscall.Close(fd)
		return nil, fmt.Errorf("bind: %w", errno)
	}

	_, _, errno = syscall.RawSyscall(
		syscall.SYS_LISTEN,
		uintptr(fd),
		128,
		0,
	)
	if errno != 0 {
		syscall.Close(fd)
		return nil, fmt.Errorf("listen: %w", errno)
	}

	file := os.NewFile(uintptr(fd), "vsock")
	return &vsockListener{file: file}, nil
}

func dialHostVsock(port uint32) (net.Conn, error) {
	const (
		AF_VSOCK        = 40
		VMADDR_CID_HOST = 2
	)
	fd, err := syscall.Socket(AF_VSOCK, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("socket: %w", err)
	}
	sa := [16]byte{}
	binary.LittleEndian.PutUint16(sa[0:2], AF_VSOCK)
	binary.LittleEndian.PutUint32(sa[4:8], port)
	binary.LittleEndian.PutUint32(sa[8:12], VMADDR_CID_HOST)
	if _, _, errno := syscall.RawSyscall(syscall.SYS_CONNECT, uintptr(fd), uintptr(unsafePointer(&sa[0])), uintptr(len(sa))); errno != 0 {
		syscall.Close(fd)
		return nil, fmt.Errorf("connect: %w", errno)
	}
	return &vsockConn{file: os.NewFile(uintptr(fd), "vsock-host")}, nil
}
