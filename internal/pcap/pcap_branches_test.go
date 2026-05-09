package pcap

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func TestNewWriterRejectsNil(t *testing.T) {
	if _, err := NewWriter(nil, 0); err == nil {
		t.Fatal("NewWriter(nil) error = nil, want non-nil")
	}
}

func TestNewWriterClampsSnaplen(t *testing.T) {
	tests := []struct {
		name    string
		snaplen int
		want    int
	}{
		{name: "zero defaults", snaplen: 0, want: DefaultSnaplen},
		{name: "negative defaults", snaplen: -1, want: DefaultSnaplen},
		{name: "exactly default", snaplen: DefaultSnaplen, want: DefaultSnaplen},
		{name: "above default clamps", snaplen: DefaultSnaplen + 1024, want: DefaultSnaplen},
		{name: "small value preserved", snaplen: 128, want: 128},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, err := NewWriter(&bytes.Buffer{}, tt.snaplen)
			if err != nil {
				t.Fatalf("NewWriter: %v", err)
			}
			if w.snaplen != tt.want {
				t.Fatalf("snaplen = %d, want %d", w.snaplen, tt.want)
			}
		})
	}
}

type errWriter struct{ err error }

func (e errWriter) Write(p []byte) (int, error) { return 0, e.err }

func TestWritePacketHeaderError(t *testing.T) {
	boom := errors.New("boom")
	w, err := NewWriter(errWriter{err: boom}, 0)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	err = w.WritePacket(time.Unix(0, 0), []byte{0x01, 0x02})
	if err == nil || !errors.Is(err, boom) {
		t.Fatalf("WritePacket header error = %v, want wrap of %v", err, boom)
	}
}

type partialWriter struct {
	buf      *bytes.Buffer
	allowHdr bool
}

func (p *partialWriter) Write(b []byte) (int, error) {
	if !p.allowHdr {
		p.allowHdr = true
		return p.buf.Write(b)
	}
	return 0, errors.New("packet write blocked")
}

func TestWritePacketBodyError(t *testing.T) {
	pw := &partialWriter{buf: &bytes.Buffer{}}
	w, err := NewWriter(pw, 0)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := w.WritePacket(time.Unix(0, 0), []byte{0x01}); err == nil {
		t.Fatal("WritePacket body error = nil, want non-nil")
	}
}

type closeRecorder struct {
	bytes.Buffer
	closed bool
}

func (c *closeRecorder) Close() error { c.closed = true; return nil }

func TestCloseClosesUnderlyingCloser(t *testing.T) {
	cr := &closeRecorder{}
	w, err := NewWriter(cr, 0)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !cr.closed {
		t.Fatal("underlying Close not called")
	}
}

func TestCloseNonCloserIsNoOp(t *testing.T) {
	w, err := NewWriter(&bytes.Buffer{}, 0)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close on non-Closer = %v, want nil", err)
	}
}
