package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/tmc/apple/foundation"
	vz "github.com/tmc/apple/virtualization"
	"github.com/tmc/vz-macos/internal/pcap"
	"golang.org/x/sys/unix"
)

// FileHandleNetworkSession owns the host-side socket and the VZ file-handle
// attachment used for filehandle network mode bring-up.
type FileHandleNetworkSession struct {
	cfg FileHandleNetworkConfig

	hostFile     *os.File
	hostConn     net.Conn
	guestHandle  foundation.NSFileHandle
	attachment   vz.VZFileHandleNetworkDeviceAttachment
	deviceConfig vz.VZVirtioNetworkDeviceConfiguration

	pcapFile *os.File
	pcap     *pcap.Writer

	stats fileHandleNetworkStats

	mu     sync.Mutex
	closed bool
}

// NewFileHandleNetworkSession creates a connected datagram socket pair,
// attaches one end to Virtualization.framework, and keeps the host end for
// frame capture or future forwarding.
func NewFileHandleNetworkSession(cfg FileHandleNetworkConfig) (*FileHandleNetworkSession, error) {
	cfg = normalizeFileHandleNetworkConfig(cfg)

	hostFD, guestFD, err := newConnectedDatagramSocketPair(cfg.MTU)
	if err != nil {
		return nil, err
	}

	return newFileHandleNetworkSessionFromFDs(hostFD, guestFD, cfg)
}

func newFileHandleNetworkSessionFromFDs(hostFD, guestFD int, cfg FileHandleNetworkConfig) (*FileHandleNetworkSession, error) {
	cfg = normalizeFileHandleNetworkConfig(cfg)
	hostFile := os.NewFile(uintptr(hostFD), "cove-filehandle-host")
	if hostFile == nil {
		_ = unix.Close(hostFD)
		_ = unix.Close(guestFD)
		return nil, fmt.Errorf("create host file")
	}

	hostConn, err := net.FileConn(hostFile)
	if err != nil {
		hostFile.Close()
		_ = unix.Close(guestFD)
		return nil, fmt.Errorf("wrap host file: %w", err)
	}

	guestHandle := newNSFileHandleFromFD(guestFD)
	attachment := vz.NewFileHandleNetworkDeviceAttachmentWithFileHandle(guestHandle)
	if attachment.ID == 0 {
		hostFile.Close()
		_, _ = guestHandle.CloseAndReturnError()
		return nil, fmt.Errorf("create filehandle network attachment")
	}
	attachment.Retain()
	attachment.SetMaximumTransmissionUnit(cfg.MTU)

	deviceConfig := vz.NewVZVirtioNetworkDeviceConfiguration()
	if deviceConfig.ID == 0 {
		hostFile.Close()
		_, _ = guestHandle.CloseAndReturnError()
		return nil, fmt.Errorf("create virtio network device configuration")
	}
	deviceConfig.SetAttachment(&attachment.VZNetworkDeviceAttachment)
	deviceConfig.Retain()

	session := &FileHandleNetworkSession{
		cfg:          cfg,
		hostFile:     hostFile,
		hostConn:     hostConn,
		guestHandle:  guestHandle,
		attachment:   attachment,
		deviceConfig: deviceConfig,
	}

	if cfg.PCAPPath != "" {
		pcapFile, err := os.Create(cfg.PCAPPath)
		if err != nil {
			session.Close()
			return nil, fmt.Errorf("open pcap %s: %w", cfg.PCAPPath, err)
		}
		writer, err := pcap.NewWriter(pcapFile, cfg.Snaplen)
		if err != nil {
			pcapFile.Close()
			session.Close()
			return nil, err
		}
		session.pcapFile = pcapFile
		session.pcap = writer
	}

	session.stats.start(time.Now())
	return session, nil
}

func normalizeFileHandleNetworkConfig(cfg FileHandleNetworkConfig) FileHandleNetworkConfig {
	if cfg.MTU <= 0 {
		cfg.MTU = defaultFileHandleMTU
	}
	if cfg.Snaplen <= 0 {
		cfg.Snaplen = pcap.DefaultSnaplen
	}
	return cfg
}

// DeviceConfiguration returns the Virtio network configuration for the session.
func (s *FileHandleNetworkSession) DeviceConfiguration() vz.VZVirtioNetworkDeviceConfiguration {
	if s == nil {
		return vz.VZVirtioNetworkDeviceConfiguration{}
	}
	return s.deviceConfig
}

// Attachment returns the underlying file-handle attachment.
func (s *FileHandleNetworkSession) Attachment() vz.VZFileHandleNetworkDeviceAttachment {
	if s == nil {
		return vz.VZFileHandleNetworkDeviceAttachment{}
	}
	return s.attachment
}

// Stats returns a snapshot of the session counters.
func (s *FileHandleNetworkSession) Stats() FileHandleNetworkStats {
	if s == nil {
		return FileHandleNetworkStats{}
	}
	return s.stats.snapshot()
}

// ReadFrame reads one datagram from the host socket.
func (s *FileHandleNetworkSession) ReadFrame() ([]byte, error) {
	if s == nil || s.hostConn == nil {
		return nil, fmt.Errorf("filehandle session not initialized")
	}
	frame, err := readFrame(s.hostConn, newFrameBuffer(s.cfg.MTU))
	if frame != nil {
		s.stats.recordInbound(len(frame))
		if s.pcap != nil {
			if werr := s.pcap.WritePacket(time.Now(), frame); werr != nil {
				return frame, werr
			}
		}
	}
	return frame, err
}

// SendFrame writes one datagram to the host socket.
func (s *FileHandleNetworkSession) SendFrame(frame []byte) error {
	if s == nil || s.hostConn == nil {
		return fmt.Errorf("filehandle session not initialized")
	}
	if _, err := writeFrame(s.hostConn, frame); err != nil {
		return fmt.Errorf("send frame: %w", err)
	}
	s.stats.recordOutbound(len(frame))
	if s.pcap != nil {
		if err := s.pcap.WritePacket(time.Now(), frame); err != nil {
			return err
		}
	}
	return nil
}

// Pump runs a host-side loop that reads guest frames and optionally emits a
// response frame generated by handler.
func (s *FileHandleNetworkSession) Pump(ctx context.Context, handler FrameProcessor) error {
	if s == nil {
		return fmt.Errorf("filehandle session not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.stats.start(time.Now())
	defer func() {
		s.stats.finish(time.Now(), nil)
	}()

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			if s.hostConn != nil {
				_ = s.hostConn.Close()
			}
		case <-done:
		}
	}()

	for {
		frame, err := s.ReadFrame()
		if len(frame) > 0 && handler != nil {
			resp, procErr := handler(ctx, frame)
			if procErr != nil {
				s.stats.finish(time.Now(), procErr)
				return fmt.Errorf("process frame: %w", procErr)
			}
			if len(resp) > 0 {
				if sendErr := s.SendFrame(resp); sendErr != nil {
					s.stats.finish(time.Now(), sendErr)
					return sendErr
				}
			}
		}
		if err != nil {
			if ctx.Err() != nil || isClosedFileError(err) {
				s.stats.finish(time.Now(), ctx.Err())
				return ctx.Err()
			}
			if err == io.EOF {
				s.stats.finish(time.Now(), nil)
				return nil
			}
			s.stats.finish(time.Now(), err)
			return fmt.Errorf("read frame: %w", err)
		}
	}
}

// Summary returns a human-readable shutdown summary.
func (s *FileHandleNetworkSession) Summary() string {
	if s == nil {
		return "filehandle network: disabled"
	}
	return s.stats.snapshot().summary(s.cfg)
}

// Close releases the host resources owned by the session.
func (s *FileHandleNetworkSession) Close() error {
	if s == nil {
		return nil
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	if s.hostConn != nil {
		_ = s.hostConn.Close()
		s.hostConn = nil
	}
	if s.hostFile != nil {
		_ = s.hostFile.Close()
		s.hostFile = nil
	}
	if s.pcap != nil {
		_ = s.pcap.Close()
		s.pcap = nil
	}
	if s.pcapFile != nil {
		_ = s.pcapFile.Close()
		s.pcapFile = nil
	}
	_, _ = s.guestHandle.CloseAndReturnError()
	s.stats.finish(time.Now(), nil)
	return nil
}

