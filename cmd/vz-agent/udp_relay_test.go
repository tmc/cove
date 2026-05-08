//go:build darwin || linux

package main

import (
	"bytes"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
)

func TestWriteUDPFrame(t *testing.T) {
	tests := []struct {
		name    string
		pkt     []byte
		wantHdr []byte
		wantErr string
	}{
		{"empty", []byte{}, []byte{0x00, 0x00}, ""},
		{"small", []byte("hi"), []byte{0x00, 0x02}, ""},
		{"max", bytes.Repeat([]byte{0xab}, 65535), []byte{0xff, 0xff}, ""},
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
			got := buf.Bytes()
			if !bytes.Equal(got[:2], tt.wantHdr) {
				t.Fatalf("hdr = %x, want %x", got[:2], tt.wantHdr)
			}
			if !bytes.Equal(got[2:], tt.pkt) {
				t.Fatalf("body mismatch")
			}
		})
	}
}

func TestReadUDPFrame(t *testing.T) {
	tests := []struct {
		name    string
		in      []byte
		want    []byte
		wantErr error // sentinel; nil means no error
	}{
		{"empty-frame", []byte{0x00, 0x00}, []byte{}, nil},
		{"small", []byte{0x00, 0x03, 'a', 'b', 'c'}, []byte("abc"), nil},
		{"truncated-hdr", []byte{0x00}, nil, io.ErrUnexpectedEOF},
		{"empty-stream", []byte{}, nil, io.EOF},
		{"truncated-body", []byte{0x00, 0x05, 'a', 'b'}, nil, io.ErrUnexpectedEOF},
		{"hdr-only-nonzero-len", []byte{0x00, 0x10}, nil, io.EOF},
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

func TestReadUDPFrameRoundTripPipe(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	want := []byte("round-trip payload")
	go func() {
		writeUDPFrame(server, want)
		server.Close()
	}()
	got, err := readUDPFrame(client)
	if err != nil {
		t.Fatalf("read1: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestReadUDPFrameMidFrameClose(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()

	// Send only the header, then close — body never arrives.
	go func() {
		server.Write([]byte{0x00, 0x10})
		server.Close()
	}()
	// Header parsed, body reads 0/N -> io.EOF (not UnexpectedEOF).
	if _, err := readUDPFrame(client); !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want io.EOF", err)
	}
}
