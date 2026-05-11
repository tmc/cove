package controlserver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
)

// Tests for readUDPFrame / writeUDPFrame error semantics. The key
// distinction is clean-close (io.EOF before any bytes) vs truncation
// (io.ErrUnexpectedEOF mid-frame), which the relay loop relies on to
// decide between graceful shutdown and logged failure.

func TestWriteUDPFrame_ControlServer(t *testing.T) {
	tests := []struct {
		name    string
		pkt     []byte
		wantHdr []byte
		wantErr string
	}{
		{"empty", []byte{}, []byte{0x00, 0x00}, ""},
		{"small", []byte("hi"), []byte{0x00, 0x02}, ""},
		{"oversize", bytes.Repeat([]byte{0}, 65536), nil, "udp packet too large"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := writeUDPFrame(&buf, tt.pkt)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want contains %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if !bytes.Equal(buf.Bytes()[:2], tt.wantHdr) {
				t.Fatalf("hdr = %x, want %x", buf.Bytes()[:2], tt.wantHdr)
			}
			if !bytes.Equal(buf.Bytes()[2:], tt.pkt) {
				t.Fatalf("body mismatch")
			}
		})
	}
}

func TestReadUDPFrame_ControlServer(t *testing.T) {
	tests := []struct {
		name    string
		in      []byte
		want    []byte
		wantErr error
	}{
		{"empty-frame", []byte{0x00, 0x00}, []byte{}, nil},
		{"small", []byte{0x00, 0x03, 'a', 'b', 'c'}, []byte("abc"), nil},
		{"empty-stream-clean-close", []byte{}, nil, io.EOF},
		{"truncated-hdr", []byte{0x00}, nil, io.ErrUnexpectedEOF},
		{"truncated-body", []byte{0x00, 0x05, 'a', 'b'}, nil, io.ErrUnexpectedEOF},
		{"hdr-only-promised-body", []byte{0x00, 0x10}, nil, io.EOF},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readUDPFrame(bytes.NewReader(tt.in))
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if !bytes.Equal(got, tt.want) {
				t.Fatalf("got %x, want %x", got, tt.want)
			}
		})
	}
}

func TestReadUDPFrame_ControlServer_MidFrameClose(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()
	go func() {
		server.Write([]byte{0x00, 0x10})
		server.Close()
	}()
	if _, err := readUDPFrame(client); !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want io.EOF", err)
	}
}

func TestPortForwardManagerCountsEmpty(t *testing.T) {
	m := NewPortForwardManager(nil)
	got := m.Counts()
	want := Counts{}
	if got != want {
		t.Fatalf("Counts on fresh manager = %+v, want %+v", got, want)
	}
	if got.Total() != 0 {
		t.Fatalf("Total = %d, want 0", got.Total())
	}
}

func TestPortForwardManagerCountsNilReceiver(t *testing.T) {
	var m *PortForwardManager
	got := m.Counts()
	if got != (Counts{}) {
		t.Fatalf("nil-receiver Counts = %+v, want zero", got)
	}
}

func TestCountsTotalSums(t *testing.T) {
	c := Counts{Forward: 2, Reverse: 1, ForwardUDP: 3, ReverseUDP: 4}
	if got, want := c.Total(), 10; got != want {
		t.Fatalf("Total = %d, want %d", got, want)
	}
}

func TestRelayPortFor(t *testing.T) {
	_ = t.TempDir()
	oldBase, oldWindow := ForwardRelayBasePort, ForwardRelayPortWindow
	t.Cleanup(func() {
		ForwardRelayBasePort = oldBase
		ForwardRelayPortWindow = oldWindow
	})
	ForwardRelayBasePort = 20000
	ForwardRelayPortWindow = 100
	tests := []struct {
		port int
		want uint32
	}{
		{0, 20000},
		{80, 20080},
		{180, 20080},
	}
	for _, tt := range tests {
		if got := relayPortFor(tt.port); got != tt.want {
			t.Fatalf("relayPortFor(%d) = %d, want %d", tt.port, got, tt.want)
		}
	}
}

func TestPortForwardManagerErrors(t *testing.T) {
	_ = t.TempDir()
	tests := []struct {
		name string
		run  func(*PortForwardManager) error
		want string
	}{
		{"start nil connector", func(m *PortForwardManager) error { return m.Start(nil, 0, 22) }, "guest connector unavailable"},
		{"start udp nil connector", func(m *PortForwardManager) error { return m.StartUDP(nil, 0, 22) }, "guest connector unavailable"},
		{"stop missing", func(m *PortForwardManager) error { return m.Stop(1234) }, "no forward on host port 1234"},
		{"reverse no listener", func(m *PortForwardManager) error { return m.StartReverse(80, 22) }, "host vsock listener not configured"},
		{"reverse udp no listener", func(m *PortForwardManager) error { return m.StartReverseUDP(80, 22) }, "host vsock listener not configured"},
	}
	old := ListenHostVsock
	t.Cleanup(func() { ListenHostVsock = old })
	ListenHostVsock = nil
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(NewPortForwardManager(nil)); err == nil || err.Error() != tt.want {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

type errConnector struct{}

func (errConnector) ConnectToGuestPort(uint32) (net.Conn, error) {
	return nil, errors.New("no guest")
}

func TestPortForwardTotalAcceptedAccumulates(t *testing.T) {
	pf := &PortForward{HostPort: 1234, GuestPort: 5678, connector: errConnector{}}
	if got := pf.TotalAccepted(); got != 0 {
		t.Fatalf("initial TotalAccepted = %d, want 0", got)
	}
	for i := 0; i < 3; i++ {
		host, peer := net.Pipe()
		_ = peer.Close()
		pf.handleConn(context.Background(), host)
	}
	if got := pf.TotalAccepted(); got != 3 {
		t.Fatalf("TotalAccepted = %d, want 3", got)
	}
}
