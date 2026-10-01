package agent

import (
	"connectrpc.com/connect"
	"context"
	pb "github.com/tmc/cove/proto/agentpb"
	"github.com/tmc/cove/proto/agentpbconnect"
	"testing"
)

type uiAgentFixture struct {
	agentpbconnect.UnimplementedUserAgentHandler
	request *pb.UIRequest
}

func (h *uiAgentFixture) UIStatus(_ context.Context, req *connect.Request[pb.UIRequest]) (*connect.Response[pb.UIResponse], error) {
	h.request = req.Msg
	return connect.NewResponse(&pb.UIResponse{Status: &pb.UIStatus{State: "permission_denied", PermissionState: "denied"}}), nil
}
func (h *uiAgentFixture) InspectUI(_ context.Context, req *connect.Request[pb.UIRequest]) (*connect.Response[pb.UIResponse], error) {
	h.request = req.Msg
	return connect.NewResponse(&pb.UIResponse{Status: &pb.UIStatus{State: "ready"}, Nodes: []*pb.UINode{{Role: "AXButton"}}}), nil
}
func (h *uiAgentFixture) FindUI(_ context.Context, req *connect.Request[pb.UIRequest]) (*connect.Response[pb.UIResponse], error) {
	h.request = req.Msg
	return connect.NewResponse(&pb.UIResponse{Status: &pb.UIStatus{State: "ready"}, MatchState: "multiple_matches"}), nil
}
func TestUserUIClientTypedReads(t *testing.T) {
	handler := &uiAgentFixture{}
	client := newTestUserAgentClient(t, handler)
	defer client.Close()
	request := &pb.UIRequest{Pid: 123, MaxNodes: 10, HostGeneration: "host", ExpectedGeneration: "guest", Label: "Save"}
	result, err := client.UIStatus(context.Background(), request)
	if err != nil || result.Status.State != "permission_denied" {
		t.Fatalf("result=%v error=%v", result, err)
	}
	result, err = client.InspectUI(context.Background(), request)
	if err != nil || len(result.Nodes) != 1 || handler.request.HostGeneration != "host" || handler.request.ExpectedGeneration != "guest" {
		t.Fatalf("result=%v request=%v error=%v", result, handler.request, err)
	}
	result, err = client.FindUI(context.Background(), request)
	if err != nil || result.MatchState != "multiple_matches" {
		t.Fatalf("result=%v error=%v", result, err)
	}
}
