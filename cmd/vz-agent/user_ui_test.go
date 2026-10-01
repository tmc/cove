package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/tmc/cove/proto/agentpb"
	"google.golang.org/protobuf/proto"
)

type fixtureUIBackend struct {
	status  *pb.UIStatus
	nodes   []*pb.UINode
	inspect func(context.Context, *pb.UIRequest) ([]*pb.UINode, bool, string, error)
	calls   int
}

func (b *fixtureUIBackend) Status(context.Context) *pb.UIStatus {
	return proto.Clone(b.status).(*pb.UIStatus)
}
func (b *fixtureUIBackend) Inspect(ctx context.Context, request *pb.UIRequest) ([]*pb.UINode, bool, string, error) {
	b.calls++
	if b.inspect != nil {
		return b.inspect(ctx, request)
	}
	return b.nodes, false, "", nil
}

func fixtureUIServer(backend *fixtureUIBackend) *userAgentServer {
	return &userAgentServer{ui: newUserUIController(backend)}
}
func readyUIBackend() *fixtureUIBackend {
	return &fixtureUIBackend{status: &pb.UIStatus{State: "ready", Backend: "fixture-ax", PermissionState: "granted", SessionState: "unlocked"}}
}

func TestUserUIDeniedOrUnavailableDoesNotInspect(t *testing.T) {
	for _, state := range []string{"permission_denied", "locked", "unsupported", "no_user_session", "unavailable"} {
		t.Run(state, func(t *testing.T) {
			backend := readyUIBackend()
			backend.status.State = state
			response, err := fixtureUIServer(backend).InspectUI(context.Background(), connect.NewRequest(&pb.UIRequest{Pid: 123}))
			if err != nil || response.Msg.Status.State != state || backend.calls != 0 {
				t.Fatalf("response=%v calls=%d error=%v", response, backend.calls, err)
			}
		})
	}
}

func TestUserUIBoundsAndDeadline(t *testing.T) {
	backend := readyUIBackend()
	server := fixtureUIServer(backend)
	for _, request := range []*pb.UIRequest{{Pid: 1, MaxNodes: 1025}, {Pid: 1, MaxDepth: 17}, {Pid: 1, MaxBytes: 300000}, {Pid: 1, MaxBytes: 100}, {Pid: 1, TimeoutMs: 10001}, {Pid: 1, TimeoutMs: 1}, {Pid: 0}} {
		if _, err := server.InspectUI(context.Background(), connect.NewRequest(request)); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("error=%v request=%v", err, request)
		}
	}
	if backend.calls != 0 {
		t.Fatal("invalid request reached native backend")
	}
	backend.inspect = func(ctx context.Context, _ *pb.UIRequest) ([]*pb.UINode, bool, string, error) {
		<-ctx.Done()
		return nil, false, "", ctx.Err()
	}
	started := time.Now()
	response, err := server.InspectUI(context.Background(), connect.NewRequest(&pb.UIRequest{Pid: 1, TimeoutMs: 100}))
	if err != nil || response.Msg.Status.State != "timed_out" || time.Since(started) > time.Second {
		t.Fatalf("response=%v duration=%s error=%v", response, time.Since(started), err)
	}
}

func TestUserUIGenerationFenceAndOpaqueObservations(t *testing.T) {
	backend := readyUIBackend()
	backend.nodes = []*pb.UINode{{Handle: "native-ref-123", Role: "AXApplication"}, {Handle: "native-ref-456", ParentHandle: "native-ref-123", Role: "AXButton", Label: "Save"}}
	server := fixtureUIServer(backend)
	request := &pb.UIRequest{Pid: 123, HostGeneration: "host-first"}
	first, err := server.InspectUI(context.Background(), connect.NewRequest(request))
	if err != nil {
		t.Fatal(err)
	}
	second, err := server.InspectUI(context.Background(), connect.NewRequest(request))
	if err != nil {
		t.Fatal(err)
	}
	if first.Msg.ObservationId == second.Msg.ObservationId || first.Msg.Nodes[0].Handle == second.Msg.Nodes[0].Handle {
		t.Fatal("observations share persistent handles")
	}
	if first.Msg.Nodes[1].ParentHandle != first.Msg.Nodes[0].Handle || strings.Contains(first.Msg.Nodes[0].Handle, "native") {
		t.Fatalf("opaque tree=%v", first.Msg.Nodes)
	}
	request = proto.Clone(request).(*pb.UIRequest)
	request.ExpectedGeneration = first.Msg.Status.Generation
	request.HostGeneration = "host-after-restore"
	response, err := server.InspectUI(context.Background(), connect.NewRequest(request))
	if err != nil || response.Msg.Status.State != "stale_generation" || len(response.Msg.Nodes) != 0 {
		t.Fatalf("stale response=%v error=%v", response, err)
	}
	if backend.calls != 2 {
		t.Fatal("stale request inspected backend")
	}
	request.HostGeneration = "host-first"
	backend.status.SessionState = "locked"
	backend.status.State = "locked"
	response, err = server.InspectUI(context.Background(), connect.NewRequest(request))
	if err != nil || response.Msg.Status.State != "stale_generation" {
		t.Fatalf("logout/lock response=%v error=%v", response, err)
	}
}

func TestUserUIFindAmbiguityAndByteBounds(t *testing.T) {
	backend := readyUIBackend()
	backend.nodes = []*pb.UINode{{Role: "AXButton", Label: "Save"}, {Role: "AXButton", Label: "Save"}, {Role: "AXButton", Label: "Cancel"}}
	server := fixtureUIServer(backend)
	response, err := server.FindUI(context.Background(), connect.NewRequest(&pb.UIRequest{Pid: 123, Label: "Save"}))
	if err != nil || response.Msg.MatchState != "multiple_matches" || len(response.Msg.Nodes) != 2 {
		t.Fatalf("matches=%v error=%v", response, err)
	}
	response, err = server.FindUI(context.Background(), connect.NewRequest(&pb.UIRequest{Pid: 123, Label: "Missing"}))
	if err != nil || response.Msg.MatchState != "no_matches" {
		t.Fatalf("matches=%v error=%v", response, err)
	}
	backend.nodes = nil
	for i := 0; i < 10; i++ {
		backend.nodes = append(backend.nodes, &pb.UINode{Role: "AXButton", Identifier: strings.Repeat("a", 1000), Label: strings.Repeat("b", 1000)})
	}
	response, err = server.InspectUI(context.Background(), connect.NewRequest(&pb.UIRequest{Pid: 123, MaxBytes: 4096}))
	if err != nil || !response.Msg.Truncated || response.Msg.TruncationReason != "byte_limit" || proto.Size(response.Msg) > 4096 {
		t.Fatalf("size=%d response=%v error=%v", proto.Size(response.Msg), response, err)
	}
	backend.inspect = func(context.Context, *pb.UIRequest) ([]*pb.UINode, bool, string, error) {
		return nil, false, "", &uiReadError{state: "target_disappeared", code: -25202}
	}
	response, err = server.InspectUI(context.Background(), connect.NewRequest(&pb.UIRequest{Pid: 123}))
	if err != nil || response.Msg.Status.State != "target_disappeared" {
		t.Fatalf("response=%v error=%v", response, err)
	}
}

func TestUserUIPrimaryReadError(t *testing.T) {
	backend := readyUIBackend()
	backend.inspect = func(context.Context, *pb.UIRequest) ([]*pb.UINode, bool, string, error) {
		return nil, false, "", errors.New("application did not respond")
	}
	response, err := fixtureUIServer(backend).InspectUI(context.Background(), connect.NewRequest(&pb.UIRequest{Pid: 123}))
	if err != nil || response.Msg.Status.State != "application_unresponsive" || len(response.Msg.Nodes) != 0 {
		t.Fatalf("response=%v error=%v", response, err)
	}
}
