package filehandle

import (
	"bytes"
	"errors"
	"io"
	"net"
	"os"
	"syscall"
	"testing"
)

func TestIsClosedFileError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "os.ErrClosed", err: os.ErrClosed, want: true},
		{name: "wrapped closed", err: &os.PathError{Op: "read", Path: "fd", Err: os.ErrClosed}, want: true},
		{name: "net closed", err: net.ErrClosed, want: true},
		{name: "bad fd", err: &os.PathError{Op: "read", Path: "fd", Err: syscall.EBADF}, want: true},
		{name: "unrelated", err: errors.New("connection refused"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isClosedFileError(tt.err); got != tt.want {
				t.Fatalf("isClosedFileError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestWriteFrame(t *testing.T) {
	var buf bytes.Buffer
	n, err := writeFrame(&buf, []byte("hello"))
	if err != nil || n != 5 {
		t.Fatalf("writeFrame: n=%d err=%v", n, err)
	}
	if buf.String() != "hello" {
		t.Fatalf("buf = %q", buf.String())
	}
	// empty frame is a no-op.
	n, err = writeFrame(&buf, nil)
	if err != nil || n != 0 {
		t.Fatalf("writeFrame(nil): n=%d err=%v", n, err)
	}
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

func TestWriteFrameShortWrite(t *testing.T) {
	n, err := writeFrame(shortWriter{}, []byte("abcd"))
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("err = %v, want io.ErrShortWrite (n=%d)", err, n)
	}
}

func TestNewFrameBufferDefault(t *testing.T) {
	if got := len(newFrameBuffer(0)); got != defaultMTU {
		t.Fatalf("len = %d, want %d", got, defaultMTU)
	}
	if got := len(newFrameBuffer(2048)); got != 2048 {
		t.Fatalf("len = %d, want 2048", got)
	}
}

func TestStatsRecord(t *testing.T) {
	var s statsState
	s.recordInbound(0)  // ignored
	s.recordOutbound(0) // ignored
	s.recordInbound(40)
	s.recordInbound(60)
	s.recordOutbound(100)
	snap := s.snapshot()
	if snap.FramesIn != 2 || snap.BytesIn != 100 {
		t.Fatalf("inbound: frames=%d bytes=%d", snap.FramesIn, snap.BytesIn)
	}
	if snap.FramesOut != 1 || snap.BytesOut != 100 {
		t.Fatalf("outbound: frames=%d bytes=%d", snap.FramesOut, snap.BytesOut)
	}
}
