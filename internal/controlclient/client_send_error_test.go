package controlclient

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

func TestSendRequestReadErrorWrapsRequestType(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "ctl-*")
	if err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "c.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		_ = conn.Close()
	}()

	c := New(sock)
	c.SetTimeout(500 * time.Millisecond)
	_, err = c.SendRequest(&controlpb.ControlRequest{Type: "screenshot"})
	if err == nil {
		t.Fatal("SendRequest: want error, got nil")
	}
	if !strings.Contains(err.Error(), `control "screenshot"`) {
		t.Fatalf("err = %v, want request type in wrap", err)
	}
}
