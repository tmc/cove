package main

import (
	"bufio"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

func TestCtlSendRequestExpiredDeadline(t *testing.T) {
	_, err := ctlSendRequestUntil("missing.sock", &controlpb.ControlRequest{Type: "agent-connect"}, time.Second, "agent-connect", time.Now().Add(-time.Second))
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded before dialing", err)
	}
}

func TestCtlSendRequestDeadlineRejectsLateResponse(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(filepath.Join(home, "tmp"), "cove-deadline-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "control.sock")
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(time.Second))
		if _, err := bufio.NewReader(conn).ReadString('\n'); err != nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
		conn.Write([]byte("{\"success\":true}\n"))
	}()
	resp, err := ctlSendRequestUntil(sock, &controlpb.ControlRequest{Type: "agent-connect"}, time.Second, "agent-connect", time.Now().Add(20*time.Millisecond))
	listener.Close()
	<-done
	if err == nil || resp != nil {
		t.Fatalf("response = %v, error = %v; accepted response after deadline", resp, err)
	}
}
