package controlclient

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	control "github.com/tmc/cove/internal/control"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

func TestSendRequestProgressCtx(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(filepath.Join(home, "tmp"), "cove-ctl-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "s")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if _, err := bufio.NewReader(conn).ReadString('\n'); err != nil {
			return
		}
		frame, _ := json.Marshal(map[string]string{"copyProgress": "64 bytes transferred"})
		control.WriteResponse(conn, &controlpb.ControlResponse{Success: true, Data: string(frame)})
		control.WriteResponse(conn, &controlpb.ControlResponse{Success: true, Data: "copied"})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var got string
	resp, err := New(socket).SendRequestProgressCtx(ctx, &controlpb.ControlRequest{Type: "agent-cp-stream"}, func(status string) { got = status })
	if err != nil {
		t.Fatal(err)
	}
	if got != "64 bytes transferred" || resp.Data != "copied" {
		t.Fatalf("status = %q, response = %q", got, resp.Data)
	}
}

func ExampleClient_SendRequestProgressCtx() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := New("unused/control.sock")
	_, err := client.SendRequestProgressCtx(ctx, &controlpb.ControlRequest{Type: "agent-cp-stream"}, func(status string) {
		fmt.Println(status)
	})
	fmt.Println(errors.Is(err, context.Canceled))
	// Output: true
}
