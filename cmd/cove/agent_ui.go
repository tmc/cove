package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"connectrpc.com/connect"
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
	user, err := s.getUserAgent()
	if err != nil {
		return uiControlResponse(&pb.UIResponse{Status: &pb.UIStatus{ProtocolVersion: 1, State: "unavailable", PermissionState: "unknown", SessionState: "unknown", Reason: "user agent unavailable; log into the guest and check agent readiness"}})
	}
	ctx, cancel := s.timeoutContext(12 * time.Second)
	defer cancel()
	var result *pb.UIResponse
	switch req.Type {
	case "agent-ui-status":
		result, err = user.UIStatus(ctx, request)
	case "agent-ui-inspect":
		result, err = user.InspectUI(ctx, request)
	case "agent-ui-find":
		result, err = user.FindUI(ctx, request)
	}
	if err != nil {
		if connect.CodeOf(err) == connect.CodeUnimplemented {
			return uiControlResponse(&pb.UIResponse{Status: &pb.UIStatus{State: "unsupported_protocol", PermissionState: "unknown", SessionState: "unknown", Reason: "installed guest user agent does not implement accessibility RPCs; upgrade the agent"}})
		}
		return &controlpb.ControlResponse{Error: fmt.Sprintf("guest user ui: %v", err)}
	}
	return uiControlResponse(result)
}
