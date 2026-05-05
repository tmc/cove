package main

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
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
		connected: make(chan struct{}),
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
