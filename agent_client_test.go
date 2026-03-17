package main

import (
	"errors"
	"io"
	"net"
	"strings"
	"testing"
)

func TestAgentClientCloseClosesUnderlyingConn(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()

	c, err := NewAgentClient(client)
	if err != nil {
		t.Fatalf("NewAgentClient() error = %v", err)
	}
	c.Close()

	buf := make([]byte, 1)
	_, err = server.Read(buf)
	if err == nil || (!errors.Is(err, io.EOF) && !strings.Contains(err.Error(), "closed pipe")) {
		t.Fatalf("server.Read() error = %v, want EOF/closed pipe", err)
	}
}
