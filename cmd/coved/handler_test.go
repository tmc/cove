package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tmc/cove/internal/coved"
)

func TestHandleStatus(t *testing.T) {
	vmRoot := t.TempDir()
	mustMkdir(t, filepath.Join(vmRoot, "one"))
	mustMkdir(t, filepath.Join(vmRoot, "two"))
	server, client := net.Pipe()
	d := &daemon{
		version:   "test-version",
		started:   time.Now().Add(-3 * time.Second),
		vmRoot:    vmRoot,
		imageGC:   coved.NewImageGCScheduler(t.TempDir(), nil),
		connected: make(chan struct{}),
	}
	if _, err := d.imageGC.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	go d.handle(server)
	if _, err := client.Write([]byte("STATUS\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	line, err := bufio.NewReader(client).ReadString('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var got statusResponse
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("unmarshal %q: %v", line, err)
	}
	if got.Version != "test-version" || got.VMsManaged != 2 {
		t.Fatalf("status = %+v", got)
	}
	if got.UptimeS < 2 {
		t.Fatalf("uptime = %d, want at least 2", got.UptimeS)
	}
	if got.ImageGCRunsTotal != 1 || got.ImageGCLastRunTS == "" {
		t.Fatalf("image gc status = %+v", got)
	}
	if got.ImageGCSkipsTotal != 0 {
		t.Fatalf("ImageGCSkipsTotal = %d, want 0 (no contention)", got.ImageGCSkipsTotal)
	}
	if got.StoragePollRunsTotal != 0 || got.StoragePollErrorsTotal != 0 {
		t.Fatalf("storage poll = (%d,%d), want zero when scheduler nil", got.StoragePollRunsTotal, got.StoragePollErrorsTotal)
	}
	if got.StorageUsedBytes != 0 {
		t.Fatalf("StorageUsedBytes = %d, want 0 when scheduler nil", got.StorageUsedBytes)
	}
	if got.WebhookDeliveredTotal != 0 || got.WebhookFailedTotal != 0 || got.WebhookRejectedTotal != 0 {
		t.Fatalf("webhook = (%d,%d,%d), want zero when subscriber nil", got.WebhookDeliveredTotal, got.WebhookFailedTotal, got.WebhookRejectedTotal)
	}
	if got.EventbusDroppedTotal != 0 || got.EventbusSubscribers != 0 {
		t.Fatalf("eventbus = (%d,%d), want zero when bus nil", got.EventbusDroppedTotal, got.EventbusSubscribers)
	}
	if got.ImageGCDurationMSTotal < 0 {
		t.Fatalf("ImageGCDurationMSTotal = %d, want >= 0", got.ImageGCDurationMSTotal)
	}
	if got.StoragePollLastRunTS != "" {
		t.Fatalf("StoragePollLastRunTS = %q, want empty when scheduler nil", got.StoragePollLastRunTS)
	}
}

func TestHandleUnknownCommand(t *testing.T) {
	server, client := net.Pipe()
	d := &daemon{connected: make(chan struct{})}
	go d.handle(server)
	if _, err := client.Write([]byte("NOPE\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	line, err := bufio.NewReader(client).ReadString('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if line != "{\"error\":\"unknown command\"}\n" {
		t.Fatalf("line = %q", line)
	}
}

func TestHandleSetsReadDeadline(t *testing.T) {
	server, client := net.Pipe()
	deadlines := make(chan time.Time, 1)
	d := &daemon{
		version:   "test-version",
		started:   time.Now(),
		vmRoot:    t.TempDir(),
		connected: make(chan struct{}),
	}
	go d.handle(deadlineConn{Conn: server, deadlines: deadlines})
	if _, err := client.Write([]byte("STATUS\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	select {
	case deadline := <-deadlines:
		if !deadline.After(time.Now()) {
			t.Fatalf("deadline = %v, want future", deadline)
		}
	case <-time.After(time.Second):
		t.Fatal("deadline was not set")
	}
}

type deadlineConn struct {
	net.Conn
	deadlines chan<- time.Time
}

func (c deadlineConn) SetReadDeadline(t time.Time) error {
	c.deadlines <- t
	return c.Conn.SetReadDeadline(t)
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
}
