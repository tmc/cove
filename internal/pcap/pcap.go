// Package pcap writes classic libpcap captures with the Ethernet link type.
package pcap

import (
	"encoding/binary"
	"fmt"
	"io"
	"sync"
	"time"
)

// DefaultSnaplen is the default maximum capture length per packet.
const DefaultSnaplen = 65535

const (
	pcapMagicLittleEndian = 0xa1b2c3d4
	pcapVersionMajor      = 2
	pcapVersionMinor      = 4
	pcapLinkTypeEthernet  = 1
)

// Writer writes classic libpcap captures with Ethernet link type.
type Writer struct {
	mu        sync.Mutex
	w         io.Writer
	snaplen   int
	network   uint32
	wroteHead bool
}

// NewWriter creates a writer that emits a pcap global header on first use.
func NewWriter(w io.Writer, snaplen int) (*Writer, error) {
	if w == nil {
		return nil, fmt.Errorf("pcap writer: nil output")
	}
	if snaplen <= 0 {
		snaplen = DefaultSnaplen
	}
	if snaplen > DefaultSnaplen {
		snaplen = DefaultSnaplen
	}
	return &Writer{
		w:       w,
		snaplen: snaplen,
		network: pcapLinkTypeEthernet,
	}, nil
}

// WritePacket writes one packet to the capture.
func (p *Writer) WritePacket(ts time.Time, packet []byte) error {
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
func (p *Writer) Close() error {
	if closer, ok := p.w.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

func (p *Writer) writeHeader() error {
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
