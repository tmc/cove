package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"time"

	"connectrpc.com/connect"
	agentstate "github.com/tmc/cove/internal/agent"
	pb "github.com/tmc/cove/proto/agentpb"
	controlpb "github.com/tmc/cove/proto/controlpb"
	"google.golang.org/protobuf/proto"
)

func (s *ControlServer) uiEpoch() (string, error) {
	s.uiMu.Lock()
	defer s.uiMu.Unlock()
	if s.uiGeneration == "" {
		var bytes [16]byte
		if _, err := rand.Read(bytes[:]); err != nil {
			return "", fmt.Errorf("generate ui runtime epoch: %w", err)
		}
		s.uiGeneration = hex.EncodeToString(bytes[:])
	}
	return s.uiGeneration, nil
}

func (s *ControlServer) invalidateUISession() {
	s.uiMu.Lock()
	s.uiGeneration = ""
	s.uiMu.Unlock()
}

func uiControlResponse(result *pb.UIResponse) *controlpb.ControlResponse {
	data, _ := protojsonMarshaler.Marshal(result)
	return &controlpb.ControlResponse{Success: true, Data: string(data), Result: &controlpb.ControlResponse_AgentUi{AgentUi: result}}
}

func (s *ControlServer) handleAgentUI(req *controlpb.ControlRequest) *controlpb.ControlResponse {
	return s.handleAgentUIWithDial(req, func(ctx context.Context, port uint32) (net.Conn, error) {
		if s.vm.ID == 0 {
			return nil, fmt.Errorf("vm unavailable")
		}
		return s.DialAgent(ctx, port)
	})
}

func (s *ControlServer) handleAgentUIWithDial(req *controlpb.ControlRequest, dial func(context.Context, uint32) (net.Conn, error)) *controlpb.ControlResponse {
	ctx, cancel := s.timeoutContext(12 * time.Second)
	defer cancel()
	request := req.GetAgentUi()
	if request == nil {
		request = &pb.UIRequest{}
	} else {
		request = proto.Clone(request).(*pb.UIRequest)
	}
	if linuxMode || windowsMode {
		return uiControlResponse(&pb.UIResponse{Status: &pb.UIStatus{ProtocolVersion: 1, State: "unsupported", PermissionState: "unsupported", SessionState: "unknown", Backend: "none", Reason: "guest accessibility currently supports macOS only"}})
	}
	epoch, err := s.uiEpoch()
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	request.HostGeneration = epoch
	var result *pb.UIResponse
	call := func(user *agentstate.UserAgentClient) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var err error
		switch req.Type {
		case "agent-ui-status":
			result, err = user.UIStatus(ctx, request)
		case "agent-ui-inspect":
			result, err = user.InspectUI(ctx, request)
		case "agent-ui-find":
			result, err = user.FindUI(ctx, request)
		}
		return err
	}
	used, err := s.bridge.WithCachedUserAgent(ctx, call)
	if err == nil && !used {
		var user *agentstate.UserAgentClient
		user, err = agentstate.NewUserAgentClientWithDial(func(callCtx context.Context) (net.Conn, error) { return dial(callCtx, agentstate.UserPort) })
		if err == nil {
			defer user.Close()
			err = call(user)
		}
	}
	if err != nil {
		if connect.CodeOf(err) == connect.CodeUnimplemented {
			return uiControlResponse(&pb.UIResponse{Status: &pb.UIStatus{State: "unsupported_protocol", PermissionState: "unknown", SessionState: "unknown", Reason: "installed guest user agent does not implement accessibility RPCs; upgrade the agent"}})
		}
		reason := "user accessibility RPC unavailable; log into the guest and check agent readiness"
		if ctx.Err() == context.DeadlineExceeded || connect.CodeOf(err) == connect.CodeDeadlineExceeded {
			reason = "user accessibility RPC timed out within the 12s diagnostic deadline"
		} else if ctx.Err() == context.Canceled {
			reason = "user accessibility RPC canceled because the VM runtime stopped"
		}
		return uiControlResponse(&pb.UIResponse{Status: &pb.UIStatus{ProtocolVersion: 1, State: "unavailable", PermissionState: "unknown", SessionState: "unknown", Reason: reason}})
	}
	return uiControlResponse(result)
}
