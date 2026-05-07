package main

import (
	"encoding/json"
	"fmt"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

// VNCStatus reports the configured VNC runtime state.
type VNCStatus struct {
	Enabled     bool   `json:"enabled"`
	Port        uint16 `json:"port,omitempty"`
	State       string `json:"state,omitempty"`
	ServiceName string `json:"service_name,omitempty"`
	Description string `json:"description,omitempty"`
}

// DebugStubStatus reports the configured debug stub runtime state.
type DebugStubStatus struct {
	Enabled     bool   `json:"enabled"`
	Kind        string `json:"kind,omitempty"`
	Port        uint16 `json:"port,omitempty"`
	ListenAll   bool   `json:"listen_all,omitempty"`
	State       string `json:"state,omitempty"`
	Description string `json:"description,omitempty"`
}

func (s *ControlServer) SetVNCStatus(status VNCStatus) {
	s.network.setVNCStatus(status)
}

func (s *ControlServer) VNCStatus() VNCStatus {
	if state := runtimeFeatureStateFor(s); state != nil {
		return state.controlVNCStatus()
	}
	return s.network.vncStatusValue()
}

func (s *ControlServer) SetDebugStubStatus(status DebugStubStatus) {
	s.network.setDebugStubStatus(status)
}

func (s *ControlServer) DebugStubStatus() DebugStubStatus {
	if state := runtimeFeatureStateFor(s); state != nil {
		return state.controlDebugStubStatus()
	}
	return s.network.debugStubStatusValue()
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
