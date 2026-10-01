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

// DisplayStatus reports configured display geometry separately from live metrics.
// Private display observation is unavailable until a safe retained display
// handle exists. ConfiguredDisplays describes explicit run configuration, not
// the guest's current display mode.
type DisplayStatus struct {
	LiveMetricsAvailable        bool                      `json:"live_metrics_available"`
	ConfigurationSource         string                    `json:"configuration_source,omitempty"`
	ConfiguredDisplays          []ConfiguredDisplayStatus `json:"configured_displays,omitempty"`
	ConfiguredDisplaysTruncated bool                      `json:"configured_displays_truncated,omitempty"`
	Available                   bool                      `json:"available"`
	Reason                      string                    `json:"reason,omitempty"`
	Class                       string                    `json:"class,omitempty"`
	Path                        string                    `json:"path,omitempty"`
	Name                        string                    `json:"name,omitempty"`
	SerialNum                   uint32                    `json:"serial_num,omitempty"`
	Port                        uint64                    `json:"port,omitempty"`
	GuestPresentCount           uint64                    `json:"guest_present_count"`
	HostPresentCount            uint64                    `json:"host_present_count"`
	CursorX                     uint16                    `json:"cursor_x"`
	CursorY                     uint16                    `json:"cursor_y"`
	SizeMillimetersWidth        float64                   `json:"size_mm_width,omitempty"`
	SizeMillimetersHeight       float64                   `json:"size_mm_height,omitempty"`
	Modes                       []DisplayModeStatus       `json:"modes,omitempty"`
}

// ConfiguredDisplayStatus describes one explicitly configured scanout.
type ConfiguredDisplayStatus struct {
	Width         int `json:"width"`
	Height        int `json:"height"`
	PixelsPerInch int `json:"pixels_per_inch,omitempty"`
}

// DisplayModeStatus describes one entry of the PGDisplay mode list.
type DisplayModeStatus struct {
	Width     uint16  `json:"width"`
	Height    uint16  `json:"height"`
	RefreshHz float64 `json:"refresh_hz"`
}

func (s *ControlServer) handleDisplayStatus() *controlpb.ControlResponse {
	return statusControlResponse(s.pgDisplayStatus())
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
