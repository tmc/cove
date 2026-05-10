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

	"github.com/tmc/vz-macos/internal/coved"
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

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
}
