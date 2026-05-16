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

func TestConfigureDatagramSocketBuffersDefaultMTU(t *testing.T) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	t.Cleanup(func() {
		unix.Close(fds[0])
		unix.Close(fds[1])
	})
	if err := configureDatagramSocketBuffers(fds[0], 0); err != nil {
		t.Fatalf("configureDatagramSocketBuffers(mtu=0): %v", err)
	}
	snd, err := unix.GetsockoptInt(fds[0], unix.SOL_SOCKET, unix.SO_SNDBUF)
	if err != nil {
		t.Fatalf("get SO_SNDBUF: %v", err)
	}
	rcv, err := unix.GetsockoptInt(fds[0], unix.SOL_SOCKET, unix.SO_RCVBUF)
	if err != nil {
		t.Fatalf("get SO_RCVBUF: %v", err)
	}
	if snd < 65536 {
		t.Errorf("SO_SNDBUF = %d, want >= 65536", snd)
	}
	if rcv < snd {
		t.Errorf("SO_RCVBUF = %d, want >= SO_SNDBUF (%d)", rcv, snd)
	}
}

func TestConfigureDatagramSocketBuffersBadFD(t *testing.T) {
	if err := configureDatagramSocketBuffers(-1, 1500); err == nil {
		t.Error("configureDatagramSocketBuffers(-1) = nil, want error")
	}
}
