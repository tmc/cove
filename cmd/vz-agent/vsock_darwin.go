package main

import (
	"fmt"
	"net"
	"os"
	"syscall"
)

// listenVsock creates a vsock listener on the given port.
// macOS: AF_VSOCK = 40, sockaddr_vm has svm_len prefix (12 bytes total).
func listenVsock(port uint32) (net.Listener, error) {
	const (
		AF_VSOCK       = 40
		VMADDR_CID_ANY = 0xFFFFFFFF
	)

	fd, err := syscall.Socket(AF_VSOCK, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("socket: %w", err)
	}

	// sockaddr_vm layout for macOS (arm64):
	//   uint8  svm_len      (1 byte)
	//   uint8  svm_family   (1 byte)
	//   uint16 svm_reserved (2 bytes)
	//   uint32 svm_port     (4 bytes)
	//   uint32 svm_cid      (4 bytes)
	sa := [12]byte{}
	sa[0] = 12       // svm_len
	sa[1] = AF_VSOCK // svm_family
	// svm_reserved = 0
	sa[4] = byte(port)
	sa[5] = byte(port >> 8)
	sa[6] = byte(port >> 16)
	sa[7] = byte(port >> 24)
	// svm_cid = VMADDR_CID_ANY
	sa[8] = 0xFF
	sa[9] = 0xFF
	sa[10] = 0xFF
	sa[11] = 0xFF

	_, _, errno := syscall.RawSyscall(
		syscall.SYS_BIND,
		uintptr(fd),
		uintptr(unsafePointer(&sa[0])),
		12,
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
