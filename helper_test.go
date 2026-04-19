package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestHelperPeerUIDAccept verifies the daemon accepts a connection from the
// authorized UID and rejects others. We run handleHelperConn directly
// (without launchd) so we can test the path without root.
func TestHelperPeerUIDAccept(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	myUID := os.Getuid()
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		handleHelperConn(conn, myUID)
	}()

	conn, err := net.DialTimeout("unix", sockPath, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))

	if err := json.NewEncoder(conn).Encode(helperRequest{Op: "ping"}); err != nil {
		t.Fatal(err)
	}
	var resp helperResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("ping rejected: %+v", resp)
	}
}

func TestHelperPeerUIDReject(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	// Allow only an impossible UID.
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		handleHelperConn(conn, -999)
	}()

	conn, err := net.DialTimeout("unix", sockPath, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))

	if err := json.NewEncoder(conn).Encode(helperRequest{Op: "ping"}); err != nil {
		// Server may have closed first; that's fine.
	}
	var resp helperResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatalf("expected error response, got decode error: %v", err)
	}
	if resp.OK {
		t.Fatalf("expected reject, got ok=true")
	}
}

func TestHelperExecScriptRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	myUID := os.Getuid()
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		handleHelperConn(conn, myUID)
	}()

	conn, err := net.DialTimeout("unix", sockPath, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	if err := json.NewEncoder(conn).Encode(helperRequest{
		Op:     "exec_script",
		Script: "#!/bin/bash\necho hello\n",
	}); err != nil {
		t.Fatal(err)
	}
	var resp helperResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("script failed: %+v", resp)
	}
	if resp.Stdout != "hello\n" {
		t.Fatalf("unexpected stdout: %q", resp.Stdout)
	}
}
