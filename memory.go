// memory.go - Memory balloon runtime control
//
// Core balloon operations delegate to vzkit. The control socket
// command handler and protobuf integration remain here.
package main

import (
	"fmt"

	"github.com/tmc/apple/dispatch"
	vz "github.com/tmc/apple/virtualization"
	balloonx "github.com/tmc/apple/x/vzkit/balloon"
	vmruntime "github.com/tmc/apple/x/vzkit/vm"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

// getMemoryInfo retrieves memory information from the VM.
func getMemoryInfo(vm vz.VZVirtualMachine, queue dispatch.Queue, vmDirectory string) (*controlpb.MemoryInfo, error) {
	info, err := balloonx.GetInfo(vmruntime.WrapQueue(queue), vm)
	if err != nil {
		return nil, err
	}
	configuredBytes, err := configuredMemoryBytes(vmDirectory)
	if err != nil {
		return nil, err
	}
	return &controlpb.MemoryInfo{
		ConfiguredGb:     bytesToGB(configuredBytes),
		HasBalloon:       info.HasBalloon,
		TargetGb:         info.TargetGB,
		MinimumAllowedMb: info.MinimumAllowMB,
	}, nil
}

// setMemoryTarget sets the memory balloon target size.
func setMemoryTarget(vm vz.VZVirtualMachine, queue dispatch.Queue, vmDirectory string, sizeGB float64) error {
	configuredBytes, err := configuredMemoryBytes(vmDirectory)
	if err != nil {
		return err
	}
	if err := validateMemoryTargetGB(sizeGB, configuredBytes); err != nil {
		return err
	}
	return balloonx.SetTarget(vmruntime.WrapQueue(queue), vm, sizeGB)
}

// handleMemoryCommand handles memory commands from the control socket.
func (s *ControlServer) handleMemoryCommand(cmd *controlpb.MemoryCommand) *controlpb.ControlResponse {
	if s.vm.ID == 0 || s.vmQueue.Handle() == 0 {
		return &controlpb.ControlResponse{Error: "VM not configured"}
	}

	switch cmd.Action {
	case "info":
		info, err := getMemoryInfo(s.vm, s.vmQueue, s.effectiveVMDir())
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
		if err := setMemoryTarget(s.vm, s.vmQueue, s.effectiveVMDir(), cmd.SizeGb); err != nil {
			return &controlpb.ControlResponse{Error: err.Error()}
		}
		msg := fmt.Sprintf("memory target set to %.2f GB", cmd.SizeGb)
		return &controlpb.ControlResponse{Success: true, Data: msg, Result: &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: msg}}}

	default:
		return &controlpb.ControlResponse{Error: fmt.Sprintf("unknown memory action: %s (use 'info' or 'set')", cmd.Action)}
	}
}
