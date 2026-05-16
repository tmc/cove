package filehandle

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/tmc/apple/x/vzkit/exp/networkfd"
)

const (
	defaultMTU = 1500
)

// FrameProcessor inspects one inbound frame and optionally returns a response
// frame to send back to the guest.
type FrameProcessor func(context.Context, []byte) ([]byte, error)

// Config configures a file-handle network attachment and host
// capture loop.
type Config struct {
	MTU      int
	Snaplen  int
	PCAPPath string
}

// Stats tracks frames flowing through the host-side loop.
type Stats struct {
	StartedAt time.Time `json:"started_at,omitempty"`
	StoppedAt time.Time `json:"stopped_at,omitempty"`
	FramesIn  uint64    `json:"frames_in,omitempty"`
	FramesOut uint64    `json:"frames_out,omitempty"`
	BytesIn   uint64    `json:"bytes_in,omitempty"`
	BytesOut  uint64    `json:"bytes_out,omitempty"`
	LastError string    `json:"last_error,omitempty"`
}

type statsState struct {
	Stats
	mu sync.Mutex
}

func (s *statsState) start(now time.Time) {
	s.mu.Lock()
	if s.StartedAt.IsZero() {
		s.StartedAt = now
	}
	s.mu.Unlock()
}

func (s *statsState) finish(now time.Time, err error) {
	s.mu.Lock()
	if s.StoppedAt.IsZero() {
		s.StoppedAt = now
	}
	if err != nil {
		s.LastError = err.Error()
	}
	s.mu.Unlock()
}

func (s *statsState) recordInbound(n int) {
	if n <= 0 {
		return
	}
	s.mu.Lock()
	s.FramesIn++
	s.BytesIn += uint64(n)
	s.mu.Unlock()
}

func (s *statsState) recordOutbound(n int) {
	if n <= 0 {
		return
	}
	s.mu.Lock()
	s.FramesOut++
	s.BytesOut += uint64(n)
	s.mu.Unlock()
}

func (s *statsState) snapshot() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Stats
}

func (s Stats) summary(cfg Config) string {
	if cfg.MTU <= 0 {
		cfg.MTU = defaultMTU
	}
	dur := time.Duration(0)
	if !s.StartedAt.IsZero() && !s.StoppedAt.IsZero() && s.StoppedAt.After(s.StartedAt) {
		dur = s.StoppedAt.Sub(s.StartedAt)
	}
	pcapState := "disabled"
	if strings.TrimSpace(cfg.PCAPPath) != "" {
		pcapState = cfg.PCAPPath
	}
	parts := []string{
		fmt.Sprintf("frames in=%d out=%d", s.FramesIn, s.FramesOut),
		fmt.Sprintf("bytes in=%d out=%d", s.BytesIn, s.BytesOut),
		fmt.Sprintf("mtu=%d", cfg.MTU),
		fmt.Sprintf("pcap=%s", pcapState),
	}
	if dur > 0 {
		parts = append(parts, fmt.Sprintf("duration=%s", dur.Round(time.Millisecond)))
	}
	if s.LastError != "" {
		parts = append(parts, "last error="+s.LastError)
	}
	return "filehandle network: " + strings.Join(parts, " ")
}

// newConnectedDatagramSocketPair returns a connected SOCK_DGRAM socket pair.
func newConnectedDatagramSocketPair(mtu int) (hostFD int, guestFD int, err error) {
	pair, err := networkfd.NewSocketPair(mtu)
	if err != nil {
		return 0, 0, err
	}
	return pair.HostFD, pair.GuestFD, nil
}

func configureDatagramSocketBuffers(fd int, mtu int) error {
	return networkfd.ConfigureDatagramSocketBuffers(fd, mtu)
}

var newNSFileHandleFromFD = networkfd.NewFileHandleFromFD

func newFrameBuffer(sizeHint int) []byte {
	if sizeHint <= 0 {
		sizeHint = defaultMTU
	}
	return make([]byte, sizeHint)
}

func readFrame(r io.Reader, buf []byte) ([]byte, error) {
	n, err := r.Read(buf)
	if n > 0 {
		frame := make([]byte, n)
		copy(frame, buf[:n])
		return frame, err
	}
	return nil, err
}

func writeFrame(w io.Writer, frame []byte) (int, error) {
	if len(frame) == 0 {
		return 0, nil
	}
	n, err := w.Write(frame)
	if err != nil {
		return n, err
	}
	if n != len(frame) {
		return n, io.ErrShortWrite
	}
	return n, nil
}

func isClosedFileError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrClosed) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "file already closed") || strings.Contains(msg, "bad file descriptor")
}
