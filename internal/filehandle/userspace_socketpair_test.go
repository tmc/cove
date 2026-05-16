package filehandle

import (
	"testing"

	"golang.org/x/sys/unix"
)

func TestNewConnectedDatagramSocketPair(t *testing.T) {
	hostFD, guestFD, err := newConnectedDatagramSocketPair(1500)
	if err != nil {
		t.Fatalf("newConnectedDatagramSocketPair: %v", err)
	}
	t.Cleanup(func() {
		unix.Close(hostFD)
		unix.Close(guestFD)
	})
	if hostFD <= 0 || guestFD <= 0 {
		t.Errorf("got fds host=%d guest=%d, want >0", hostFD, guestFD)
	}
	msg := []byte("ping")
	if _, err := unix.Write(hostFD, msg); err != nil {
		t.Fatalf("write to host: %v", err)
	}
	buf := make([]byte, 64)
	n, err := unix.Read(guestFD, buf)
	if err != nil {
		t.Fatalf("read from guest: %v", err)
	}
	if string(buf[:n]) != "ping" {
		t.Errorf("got %q, want %q", buf[:n], "ping")
	}
}
