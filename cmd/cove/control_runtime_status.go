package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

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

// RuntimeServerInfo describes the process that owns the VM control socket.
type RuntimeServerInfo struct {
	Executable    string `json:"executable,omitempty"`
	PID           int    `json:"pid"`
	PPID          int    `json:"ppid,omitempty"`
	SessionID     int    `json:"session_id,omitempty"`
	Command       string `json:"command,omitempty"`
	ParentCommand string `json:"parent_command,omitempty"`
	StartSource   string `json:"start_source,omitempty"`
	StartedAt     string `json:"started_at,omitempty"`
	Version       string `json:"version"`
	Commit        string `json:"commit"`
	Date          string `json:"date"`
	VMDir         string `json:"vm_dir,omitempty"`
	SocketPath    string `json:"socket_path,omitempty"`
}

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

func (s *ControlServer) handleServerInfo() *controlpb.ControlResponse {
	exe, _ := os.Executable()
	info := resolvedVersion()
	owner := currentRuntimeOwnerInfo()
	startedAt, _, _ := s.policySnapshot()
	startedAtString := ""
	if !startedAt.IsZero() {
		startedAtString = startedAt.Format(time.RFC3339)
	} else if !owner.StartedAt.IsZero() {
		startedAtString = owner.StartedAt.Format(time.RFC3339)
	}
	return statusControlResponse(RuntimeServerInfo{
		Executable:    exe,
		PID:           owner.PID,
		PPID:          owner.PPID,
		SessionID:     owner.SessionID,
		Command:       owner.Command,
		ParentCommand: owner.ParentCommand,
		StartSource:   owner.StartSource,
		StartedAt:     startedAtString,
		Version:       info.Version,
		Commit:        info.Commit,
		Date:          info.Date,
		VMDir:         s.effectiveVMDir(),
		SocketPath:    s.socketPath,
	})
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
