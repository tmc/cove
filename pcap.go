package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"sync"
	"time"
)

const (
	pcapMagicLittleEndian = 0xa1b2c3d4
	pcapVersionMajor      = 2
	pcapVersionMinor      = 4
	pcapLinkTypeEthernet  = 1
	pcapDefaultSnaplen    = 65535
)

// PCAPWriter writes classic libpcap captures with Ethernet link type.
type PCAPWriter struct {
	mu        sync.Mutex
	w         io.Writer
	snaplen   int
	network   uint32
	wroteHead bool
}

// NewPCAPWriter creates a writer that emits a PCAP global header on first use.
func NewPCAPWriter(w io.Writer, snaplen int) (*PCAPWriter, error) {
	if w == nil {
		return nil, fmt.Errorf("pcap writer: nil output")
	}
	if snaplen <= 0 {
		snaplen = pcapDefaultSnaplen
	}
	if snaplen > pcapDefaultSnaplen {
		snaplen = pcapDefaultSnaplen
	}
	return &PCAPWriter{
		w:       w,
		snaplen: snaplen,
		network: pcapLinkTypeEthernet,
	}, nil
}

// WritePacket writes one packet to the capture.
func (p *PCAPWriter) WritePacket(ts time.Time, packet []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.wroteHead {
		if err := p.writeHeader(); err != nil {
			return err
		}
	}

	origLen := len(packet)
	inclLen := origLen
	if inclLen > p.snaplen {
		inclLen = p.snaplen
		packet = packet[:inclLen]
	}

	var record [16]byte
	binary.LittleEndian.PutUint32(record[0:4], uint32(ts.Unix()))
	binary.LittleEndian.PutUint32(record[4:8], uint32(ts.Nanosecond()/1000))
	binary.LittleEndian.PutUint32(record[8:12], uint32(inclLen))
	binary.LittleEndian.PutUint32(record[12:16], uint32(origLen))

	if _, err := p.w.Write(record[:]); err != nil {
		return fmt.Errorf("write pcap packet header: %w", err)
	}
	if _, err := p.w.Write(packet); err != nil {
		return fmt.Errorf("write pcap packet data: %w", err)
	}
	return nil
}

// Close flushes the writer if it exposes a Close method.
func (p *PCAPWriter) Close() error {
	if closer, ok := p.w.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

func (p *PCAPWriter) writeHeader() error {
	var hdr [24]byte
	binary.LittleEndian.PutUint32(hdr[0:4], pcapMagicLittleEndian)
	binary.LittleEndian.PutUint16(hdr[4:6], pcapVersionMajor)
	binary.LittleEndian.PutUint16(hdr[6:8], pcapVersionMinor)
	binary.LittleEndian.PutUint32(hdr[8:12], 0)
	binary.LittleEndian.PutUint32(hdr[12:16], 0)
	binary.LittleEndian.PutUint32(hdr[16:20], uint32(p.snaplen))
	binary.LittleEndian.PutUint32(hdr[20:24], p.network)
	if _, err := p.w.Write(hdr[:]); err != nil {
		return fmt.Errorf("write pcap header: %w", err)
	}
	p.wroteHead = true
	return nil
}
