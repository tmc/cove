package main

import (
	"encoding/json"
	"fmt"

	"github.com/tmc/cove/internal/controlserver"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

// VNCStatus is an alias of controlserver.VNCStatus. The type lives in
// internal/controlserver so the network bridge (extracted next) can
// hold it without crossing the package-main boundary.
type VNCStatus = controlserver.VNCStatus

// DebugStubStatus is an alias of controlserver.DebugStubStatus. See
// VNCStatus for the placement rationale.
type DebugStubStatus = controlserver.DebugStubStatus

func (s *ControlServer) SetVNCStatus(status VNCStatus) {
	s.network.SetVNCStatus(status)
}

func (s *ControlServer) VNCStatus() VNCStatus {
	if state := runtimeFeatureStateFor(s); state != nil {
		return state.controlVNCStatus()
	}
	return s.network.VNCStatusValue()
}

func (s *ControlServer) SetDebugStubStatus(status DebugStubStatus) {
	s.network.SetDebugStubStatus(status)
}

func (s *ControlServer) DebugStubStatus() DebugStubStatus {
	if state := runtimeFeatureStateFor(s); state != nil {
		return state.controlDebugStubStatus()
	}
	return s.network.DebugStubStatusValue()
}

func (s *ControlServer) handleVNCStatus() *controlpb.ControlResponse {
	return statusControlResponse(s.VNCStatus())
}

func (s *ControlServer) handleDebugStubStatus() *controlpb.ControlResponse {
	return statusControlResponse(s.DebugStubStatus())
}

func statusControlResponse(value any) *controlpb.ControlResponse {
	data, err := json.Marshal(value)
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("marshal status: %v", err)}
	}
	return &controlpb.ControlResponse{
		Success: true,
		Data:    string(data),
	}
}
