package main

import (
	"net"
	"os"
	"syscall"
	"time"
)

type vsockListener struct {
	file *os.File
}

func (l *vsockListener) Accept() (net.Conn, error) {
	// Blocking accept since the file is in blocking mode
	fd, _, errno := syscall.Syscall(syscall.SYS_ACCEPT, l.file.Fd(), 0, 0)
	if errno != 0 {
		return nil, errno
	}
	f := os.NewFile(uintptr(fd), "vsock-conn")
	return &vsockConn{file: f}, nil
}

func (l *vsockListener) Close() error {
	return l.file.Close()
}

func (l *vsockListener) Addr() net.Addr {
	return vsockAddr{}
}

type vsockConn struct {
	file *os.File
}

func (c *vsockConn) Read(b []byte) (n int, err error) {
	return c.file.Read(b)
}

func (c *vsockConn) Write(b []byte) (n int, err error) {
	return c.file.Write(b)
}

func (c *vsockConn) Close() error {
	return c.file.Close()
}

func (c *vsockConn) LocalAddr() net.Addr {
	return vsockAddr{}
}

func (c *vsockConn) RemoteAddr() net.Addr {
	return vsockAddr{}
}

func (c *vsockConn) SetDeadline(t time.Time) error {
	return c.file.SetDeadline(t)
}

func (c *vsockConn) SetReadDeadline(t time.Time) error {
	return c.file.SetReadDeadline(t)
}

func (c *vsockConn) SetWriteDeadline(t time.Time) error {
	return c.file.SetWriteDeadline(t)
}

type vsockAddr struct{}

func (vsockAddr) Network() string { return "vsock" }
func (vsockAddr) String() string  { return "vsock" }
