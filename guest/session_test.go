package guest

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	control "github.com/tmc/cove/internal/control"
	pb "github.com/tmc/cove/proto/agentpb"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

func sessionFixture(t *testing.T, handle func(*controlpb.ControlRequest) *controlpb.ControlResponse) *Session {
	t.Helper()
	root, err := os.MkdirTemp(filepath.Join(os.Getenv("HOME"), "tmp"), "cove-session-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	socket := filepath.Join(root, "s")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			func() {
				defer conn.Close()
				line, err := bufio.NewReader(conn).ReadBytes('\n')
				if err != nil {
					return
				}
				var request controlpb.ControlRequest
				if err := control.ProtoJSONUnmarshaler.Unmarshal(line, &request); err != nil {
					return
				}
				response := handle(&request)
				if response == nil {
					var buffer [1]byte
					conn.Read(buffer[:])
					return
				}
				data, err := control.ProtoJSONMarshaler.Marshal(response)
				if err != nil {
					return
				}
				conn.Write(append(data, '\n'))
			}()
		}
	}()
	session, err := NewSession(socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func TestSessionTypedReadOnlyOperations(t *testing.T) {
	requests := make(chan *controlpb.ControlRequest, 3)
	session := sessionFixture(t, func(request *controlpb.ControlRequest) *controlpb.ControlResponse {
		requests <- request
		return &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_AgentUi{AgentUi: &pb.UIResponse{Status: &pb.UIStatus{State: "permission_denied", PermissionState: "denied", Generation: "generation"}, MatchState: "multiple_matches", Nodes: []*pb.UINode{{Handle: "opaque", Role: "AXButton", ValueOmitted: true}}}}}
	})
	status, err := session.Ready(context.Background())
	if err != nil || status.State != "permission_denied" {
		t.Fatalf("status=%+v error=%v", status, err)
	}
	query := Query{PID: 123, Role: "AXButton", ExpectedGeneration: "old", MaxNodes: 10, Timeout: time.Second}
	observation, err := session.Inspect(context.Background(), query)
	if err != nil || len(observation.Nodes) != 1 || !observation.Nodes[0].ValueOmitted {
		t.Fatalf("observation=%+v error=%v", observation, err)
	}
	observation, err = session.Find(context.Background(), query)
	if err != nil || observation.MatchState != "multiple_matches" {
		t.Fatalf("observation=%+v error=%v", observation, err)
	}
	for _, kind := range []string{"agent-ui-status", "agent-ui-inspect", "agent-ui-find"} {
		request := <-requests
		if request.Type != kind {
			t.Fatalf("request=%v", request)
		}
		if kind != "agent-ui-status" && (request.GetAgentUi().Pid != 123 || request.GetAgentUi().ExpectedGeneration != "old" || request.GetAgentUi().MaxNodes != 10) {
			t.Fatalf("query lost: %v", request)
		}
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Ready(context.Background()); err == nil {
		t.Fatal("closed session sent request")
	}
}

func TestSessionCloseCancelsClientTransport(t *testing.T) {
	accepted := make(chan struct{})
	session := sessionFixture(t, func(*controlpb.ControlRequest) *controlpb.ControlResponse { close(accepted); return nil })
	done := make(chan error, 1)
	go func() { _, err := session.Ready(context.Background()); done <- err }()
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("request not received")
	}
	session.Close()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("closed session leaked client transport")
	}
}

func TestSessionRefusesUninitializedAndBadTimeout(t *testing.T) {
	var session *Session
	if _, err := session.Ready(context.Background()); err == nil {
		t.Fatal("nil session accepted")
	}
	session, err := NewSession("unused")
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if _, err := session.Inspect(context.Background(), Query{PID: 1, Timeout: 20 * time.Second}); err == nil {
		t.Fatal("bad timeout accepted")
	}
}

func TestSessionBoundsResponseBytes(t *testing.T) {
	session := sessionFixture(t, func(*controlpb.ControlRequest) *controlpb.ControlResponse {
		return &controlpb.ControlResponse{Success: true, Data: strings.Repeat("x", (4<<20)+1)}
	})
	_, err := session.Ready(context.Background())
	if err == nil || !strings.Contains(err.Error(), "response exceeds") {
		t.Fatalf("oversized response error=%v", err)
	}
}
