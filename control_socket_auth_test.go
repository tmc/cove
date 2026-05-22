package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

func TestControlServerAuthRejectsMissingToken(t *testing.T) {
	dir := t.TempDir()
	sock := shortTestSocketPath(t)
	defer os.Remove(sock)

	s := NewControlServerWithVMDir(sock, dir)
	s.authToken = "secret-token"
	if err := s.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Stop()

	waitForControlSocket(t, sock)

	resp := sendControlLine(t, sock, `{"type":"ping"}`)
	if resp.Error != "unauthorized" {
		t.Fatalf("error = %q, want unauthorized", resp.Error)
	}

	resp = sendControlLine(t, sock, `{"type":"ping","token":"secret-token"}`)
	if !resp.Success || resp.Data != "pong" {
		t.Fatalf("legacy token ping = %#v, want success pong", resp)
	}

	resp = sendControlLine(t, sock, `{"type":"ping","auth_token":"secret-token"}`)
	if !resp.Success || resp.Data != "pong" {
		t.Fatalf("auth_token ping = %#v, want success pong", resp)
	}
}

func TestControlServerStartCreatesTokenAndLocksSocketPermissions(t *testing.T) {
	dir := t.TempDir()
	sock := shortTestSocketPath(t)
	defer os.Remove(sock)

	s := NewControlServerWithVMDir(sock, dir)
	if err := s.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Stop()

	if s.authToken == "" {
		t.Fatalf("auth token not set")
	}

	tokenPath := GetControlTokenPathForVM(dir)
	token, err := LoadControlTokenFromPath(tokenPath)
	if err != nil {
		t.Fatalf("read token: %v", err)
	}
	if token != s.authToken {
		t.Fatalf("token mismatch: file=%q server=%q", token, s.authToken)
	}

	tokenInfo, err := os.Stat(tokenPath)
	if err != nil {
		t.Fatalf("stat token: %v", err)
	}
	if tokenInfo.Mode().Perm() != 0600 {
		t.Fatalf("token perms = %04o, want 0600", tokenInfo.Mode().Perm())
	}

	socketInfo, err := os.Stat(sock)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if socketInfo.Mode().Perm() != 0600 {
		t.Fatalf("socket perms = %04o, want 0600", socketInfo.Mode().Perm())
	}
}

func waitForControlSocket(t *testing.T, sock string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", sock, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("socket not ready: %s", sock)
}

func sendControlLine(t *testing.T, sock, line string) *controlpb.ControlResponse {
	t.Helper()
	conn, err := net.DialTimeout("unix", sock, time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte(line + "\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	respLine, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var resp controlpb.ControlResponse
	if err := protojson.Unmarshal([]byte(respLine), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return &resp
}

func shortTestSocketPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(os.TempDir(), fmt.Sprintf("vzmacos-test-%d.sock", time.Now().UnixNano()))
}
