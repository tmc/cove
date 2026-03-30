package main

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"
)

func TestPCAPWriterWritesHeaderAndPacket(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewPCAPWriter(&buf, 16)
	if err != nil {
		t.Fatalf("new pcap writer: %v", err)
	}

	packet := []byte("hello world")
	ts := time.Unix(1700000000, 123456000)
	if err := w.WritePacket(ts, packet); err != nil {
		t.Fatalf("write packet: %v", err)
	}

	got := buf.Bytes()
	if len(got) != 24+16+len(packet) {
		t.Fatalf("pcap size = %d, want %d", len(got), 24+16+len(packet))
	}

	if magic := binary.LittleEndian.Uint32(got[0:4]); magic != pcapMagicLittleEndian {
		t.Fatalf("magic = %#x, want %#x", magic, pcapMagicLittleEndian)
	}
	if major := binary.LittleEndian.Uint16(got[4:6]); major != pcapVersionMajor {
		t.Fatalf("major = %d, want %d", major, pcapVersionMajor)
	}
	if minor := binary.LittleEndian.Uint16(got[6:8]); minor != pcapVersionMinor {
		t.Fatalf("minor = %d, want %d", minor, pcapVersionMinor)
	}
	if snaplen := binary.LittleEndian.Uint32(got[16:20]); snaplen != 16 {
		t.Fatalf("snaplen = %d, want 16", snaplen)
	}
	if network := binary.LittleEndian.Uint32(got[20:24]); network != pcapLinkTypeEthernet {
		t.Fatalf("network = %d, want %d", network, pcapLinkTypeEthernet)
	}

	rec := got[24:]
	if tsSec := binary.LittleEndian.Uint32(rec[0:4]); tsSec != uint32(ts.Unix()) {
		t.Fatalf("ts_sec = %d, want %d", tsSec, ts.Unix())
	}
	if tsUsec := binary.LittleEndian.Uint32(rec[4:8]); tsUsec != uint32(ts.Nanosecond()/1000) {
		t.Fatalf("ts_usec = %d, want %d", tsUsec, ts.Nanosecond()/1000)
	}
	if inclLen := binary.LittleEndian.Uint32(rec[8:12]); inclLen != 11 {
		t.Fatalf("incl_len = %d, want 11", inclLen)
	}
	if origLen := binary.LittleEndian.Uint32(rec[12:16]); origLen != 11 {
		t.Fatalf("orig_len = %d, want 11", origLen)
	}
	if payload := string(rec[16:]); payload != "hello world" {
		t.Fatalf("payload = %q, want %q", payload, "hello world")
	}
}

func TestPCAPWriterTruncatesToSnaplen(t *testing.T) {
	var buf bytes.Buffer
	w, err := NewPCAPWriter(&buf, 4)
	if err != nil {
		t.Fatalf("new pcap writer: %v", err)
	}

	if err := w.WritePacket(time.Unix(0, 0), []byte("abcdef")); err != nil {
		t.Fatalf("write packet: %v", err)
	}

	got := buf.Bytes()
	if len(got) != 24+16+4 {
		t.Fatalf("pcap size = %d, want %d", len(got), 24+16+4)
	}
	if inclLen := binary.LittleEndian.Uint32(got[24+8 : 24+12]); inclLen != 4 {
		t.Fatalf("incl_len = %d, want 4", inclLen)
	}
	if origLen := binary.LittleEndian.Uint32(got[24+12 : 24+16]); origLen != 6 {
		t.Fatalf("orig_len = %d, want 6", origLen)
	}
	if payload := string(got[24+16:]); payload != "abcd" {
		t.Fatalf("payload = %q, want %q", payload, "abcd")
	}
}
