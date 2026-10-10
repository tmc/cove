package main

import (
	"bufio"
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

func TestWorkspaceStreamDeadlineWire(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	want, _ := ctx.Deadline()
	socket := workspaceStreamFixture(t, func(conn net.Conn, req *controlpb.ControlRequest) {
		if cmd := req.GetAgentExec(); cmd == nil || cmd.DeadlineUnixNano != want.UnixNano() {
			t.Error("stream caller deadline absent or replaced")
		}
		writeResponse(conn, &controlpb.ControlResponse{Error: "command not dispatched"})
	})
	_, err := streamWorkspaceTask(ctx, socket, []string{"true"}, "", time.Minute, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "command not dispatched") {
		t.Fatalf("stream error %v", err)
	}
}

func TestAgentStreamExpiredBudgetNoAcquisition(t *testing.T) {
	for _, route := range []string{"agent-user-exec-stream", "agent-exec-stream"} {
		t.Run(route, func(t *testing.T) {
			server, client := net.Pipe()
			defer client.Close()
			client.SetDeadline(time.Now().Add(time.Second))
			s := NewControlServerWithVMDir("", t.TempDir())
			// An unconfigured native VM would fail if acquisition ran.
			req := &controlpb.ControlRequest{Type: route, Command: &controlpb.ControlRequest_AgentExec{AgentExec: &controlpb.AgentExecCommand{Args: []string{"true"}, DeadlineUnixNano: time.Now().Add(-time.Second).UnixNano()}}}
			done := make(chan struct{})
			go func() { defer close(done); defer server.Close(); s.handleAgentExecStreamConnection(server, req) }()
			line, err := bufio.NewReader(client).ReadString('\n')
			if err != nil || !strings.Contains(line, "command not dispatched") || !strings.Contains(line, "budget expired") {
				t.Fatalf("response %q, error %v", line, err)
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("expired handler did not return")
			}
		})
	}
}
