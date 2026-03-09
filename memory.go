// memory.go - Memory balloon runtime control
//
// Core balloon operations delegate to vzkit. The control socket
// command handler and protobuf integration remain here.
package main

import (
	"fmt"

	"github.com/tmc/apple/dispatch"
	vz "github.com/tmc/apple/virtualization"
	"github.com/tmc/apple/x/vzkit"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

// getMemoryInfo retrieves memory information from the VM.
func getMemoryInfo(vm vz.VZVirtualMachine, queue dispatch.Queue) (*controlpb.MemoryInfo, error) {
	info, err := vzkit.GetBalloonInfo(vzkit.WrapQueue(queue), vm)
	if err != nil {
		return nil, err
	}
	return &controlpb.MemoryInfo{
		HasBalloon:       info.HasBalloon,
		TargetGb:         info.TargetGB,
		MinimumAllowedMb: info.MinimumAllowMB,
	}, nil
}

// setMemoryTarget sets the memory balloon target size.
func setMemoryTarget(vm vz.VZVirtualMachine, queue dispatch.Queue, sizeGB float64) error {
	return vzkit.SetBalloonTarget(vzkit.WrapQueue(queue), vm, sizeGB)
}

// handleMemoryCommand handles memory commands from the control socket.
func (s *ControlServer) handleMemoryCommand(cmd *controlpb.MemoryCommand) *controlpb.ControlResponse {
	if s.vm.ID == 0 || s.vmQueue.Handle() == 0 {
		return &controlpb.ControlResponse{Error: "VM not configured"}
	}

	switch cmd.Action {
	case "info":
		info, err := getMemoryInfo(s.vm, s.vmQueue)
		if err != nil {
			return &controlpb.ControlResponse{Error: err.Error()}
		}
		data, _ := protojsonMarshaler.Marshal(info)
		return &controlpb.ControlResponse{
			Success: true,
			Data:    string(data),
			Result:  &controlpb.ControlResponse_MemoryInfo{MemoryInfo: &controlpb.MemoryInfoResponse{Info: info}},
		}

	case "set":
		if cmd.SizeGb <= 0 {
			return &controlpb.ControlResponse{Error: "size_gb must be positive"}
		}
		if err := setMemoryTarget(s.vm, s.vmQueue, cmd.SizeGb); err != nil {
			return &controlpb.ControlResponse{Error: err.Error()}
		}
		msg := fmt.Sprintf("memory target set to %.2f GB", cmd.SizeGb)
		return &controlpb.ControlResponse{Success: true, Data: msg, Result: &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: msg}}}

	default:
		return &controlpb.ControlResponse{Error: fmt.Sprintf("unknown memory action: %s (use 'info' or 'set')", cmd.Action)}
	}
}

// addMemoryBalloonDevice adds a memory balloon device to a VM config.
func addMemoryBalloonDevice(config vz.VZVirtualMachineConfiguration) {
	vzkit.AddMemoryBalloonDevice(config)
}
