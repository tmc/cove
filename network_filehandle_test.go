package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tmc/vz-macos/internal/pcap"
	"golang.org/x/sys/unix"
)

func TestFileHandleNetworkSessionCreatesAttachment(t *testing.T) {
	session, err := NewFileHandleNetworkSession(FileHandleNetworkConfig{
		MTU:      2048,
		Snaplen:  4096,
		PCAPPath: "",
	})
	if err != nil {
		t.Skipf("filehandle network session unavailable: %v", err)
	}
	defer session.Close()

	if got := session.Attachment().MaximumTransmissionUnit(); got != 2048 {
		t.Fatalf("maximum transmission unit = %d, want 2048", got)
	}
	if session.DeviceConfiguration().ID == 0 {
		t.Fatal("device configuration has zero ID")
	}
}

func TestFileHandleNetworkSessionPumpEcho(t *testing.T) {
	hostFD, guestFD, err := newConnectedDatagramSocketPair(2048)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	dupFD, err := unix.Dup(guestFD)
	if err != nil {
		unix.Close(hostFD)
		unix.Close(guestFD)
		t.Fatalf("dup guest fd: %v", err)
	}

	session, err := newFileHandleNetworkSessionFromFDs(hostFD, guestFD, FileHandleNetworkConfig{
		MTU:     2048,
		Snaplen: 4096,
	})
	if err != nil {
		unix.Close(dupFD)
		t.Fatalf("session: %v", err)
	}
	defer session.Close()

	guest := os.NewFile(uintptr(dupFD), "guest-test")
	if guest == nil {
		t.Fatal("create guest test file")
	}
	defer guest.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- session.Pump(ctx, func(context.Context, []byte) ([]byte, error) {
			return []byte("ack-ping"), nil
		})
	}()

	if _, err := guest.Write([]byte("ping")); err != nil {
		t.Fatalf("guest write: %v", err)
	}

	buf := make([]byte, 64)
	n, err := guest.Read(buf)
	if err != nil {
		t.Fatalf("guest read: %v", err)
	}
	if got := string(buf[:n]); got != "ack-ping" {
		t.Fatalf("guest response = %q, want %q", got, "ack-ping")
	}

	cancel()
	_ = guest.Close()

	select {
	case pumpErr := <-errCh:
		if pumpErr != nil && !errors.Is(pumpErr, context.Canceled) {
			t.Fatalf("pump error: %v", pumpErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pump did not stop")
	}

	stats := session.Stats()
	if stats.FramesIn != 1 || stats.FramesOut != 1 {
		t.Fatalf("stats = %+v, want one in and one out frame", stats)
	}
	if stats.BytesIn != 4 || stats.BytesOut != 8 {
		t.Fatalf("stats = %+v, want 4 bytes in and 8 bytes out", stats)
	}

	summary := session.Summary()
	for _, want := range []string{"frames in=1 out=1", "bytes in=4 out=8", "mtu=2048"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary %q does not contain %q", summary, want)
		}
	}
}

func TestNormalizeFileHandleNetworkConfig(t *testing.T) {
	cfg := normalizeFileHandleNetworkConfig(FileHandleNetworkConfig{})
	if cfg.MTU != defaultFileHandleMTU {
		t.Fatalf("mtu = %d, want %d", cfg.MTU, defaultFileHandleMTU)
	}
	if cfg.Snaplen != pcap.DefaultSnaplen {
		t.Fatalf("snaplen = %d, want %d", cfg.Snaplen, pcap.DefaultSnaplen)
	}
}

func TestFileHandleNetworkSummaryIncludesPcap(t *testing.T) {
	stats := FileHandleNetworkStats{
		StartedAt: time.Unix(100, 0),
		StoppedAt: time.Unix(101, 250000000),
		FramesIn:  3,
		FramesOut: 2,
		BytesIn:   512,
		BytesOut:  256,
	}
	summary := stats.summary(FileHandleNetworkConfig{MTU: 1500, PCAPPath: "/tmp/test.pcap"})
	for _, want := range []string{"frames in=3 out=2", "bytes in=512 out=256", "pcap=/tmp/test.pcap"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary %q does not contain %q", summary, want)
		}
	}
}
