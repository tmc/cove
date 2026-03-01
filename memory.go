// memory.go - Memory balloon runtime control
package main

import (
	"fmt"

	"github.com/tmc/apple/dispatch"
	vz "github.com/tmc/apple/virtualization"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

// getMemoryInfo retrieves memory information from the VM
func getMemoryInfo(vm vz.VZVirtualMachine, queue dispatch.Queue) (*controlpb.MemoryInfo, error) {
	var info controlpb.MemoryInfo
	var err error

	done := make(chan struct{})
	DispatchAsyncQueue(queue, func() {
		defer close(done)

		// Get memory balloon devices
		devices := vm.MemoryBalloonDevices()
		if len(devices) == 0 {
			info.HasBalloon = false
			return
		}

		info.HasBalloon = true

		// Get the first balloon device and cast to traditional balloon type
		device := devices[0]
		// Cast to VZVirtioTraditionalMemoryBalloonDevice
		traditionalDevice := vz.VZVirtioTraditionalMemoryBalloonDeviceFromID(device.GetID())
		if traditionalDevice.ID == 0 {
			err = fmt.Errorf("balloon device is not a VirtioTraditional type")
			return
		}

		// Get current target
		targetBytes := traditionalDevice.TargetVirtualMachineMemorySize()
		info.TargetGb = float64(targetBytes) / (1024 * 1024 * 1024)

		// Get minimum allowed
		info.MinimumAllowedMb = vz.GetVZVirtualMachineConfigurationClass().MinimumAllowedMemorySize() / (1024 * 1024)
	})
	<-done

	if err != nil {
		return nil, err
	}
	return &info, nil
}

// setMemoryTarget sets the memory balloon target size
func setMemoryTarget(vm vz.VZVirtualMachine, queue dispatch.Queue, sizeGB float64) error {
	sizeBytes := uint64(sizeGB * 1024 * 1024 * 1024)

	// Ensure size is a multiple of 1MB
	sizeBytes = (sizeBytes / (1024 * 1024)) * (1024 * 1024)

	var err error
	done := make(chan struct{})
	DispatchAsyncQueue(queue, func() {
		defer close(done)

		devices := vm.MemoryBalloonDevices()
		if len(devices) == 0 {
			err = fmt.Errorf("no memory balloon device configured")
			return
		}

		device := devices[0]
		traditionalDevice := vz.VZVirtioTraditionalMemoryBalloonDeviceFromID(device.GetID())
		if traditionalDevice.ID == 0 {
			err = fmt.Errorf("balloon device is not a VirtioTraditional type")
			return
		}

		// Validate the size
		minSize := vz.GetVZVirtualMachineConfigurationClass().MinimumAllowedMemorySize()
		if sizeBytes < minSize {
			err = fmt.Errorf("target size %.2f GB is below minimum allowed %.2f GB",
				sizeGB, float64(minSize)/(1024*1024*1024))
			return
		}

		// Set the target
		traditionalDevice.SetTargetVirtualMachineMemorySize(sizeBytes)
	})
	<-done

	return err
}

// handleMemoryCommand handles memory commands from the control socket
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
		return &controlpb.ControlResponse{Success: true, Data: string(data)}

	case "set":
		if cmd.SizeGb <= 0 {
			return &controlpb.ControlResponse{Error: "size_gb must be positive"}
		}
		if err := setMemoryTarget(s.vm, s.vmQueue, cmd.SizeGb); err != nil {
			return &controlpb.ControlResponse{Error: err.Error()}
		}
		return &controlpb.ControlResponse{Success: true, Data: fmt.Sprintf("memory target set to %.2f GB", cmd.SizeGb)}

	default:
		return &controlpb.ControlResponse{Error: fmt.Sprintf("unknown memory action: %s (use 'info' or 'set')", cmd.Action)}
	}
}

// addMemoryBalloonToMacOSConfig adds a memory balloon device to a macOS VM config
// Call this from buildVMConfiguration to enable runtime memory control
func addMemoryBalloonDevice(config vz.VZVirtualMachineConfiguration) {
	balloonConfig := vz.NewVZVirtioTraditionalMemoryBalloonDeviceConfiguration()
	if balloonConfig.ID != 0 {
		config.SetMemoryBalloonDevices([]vz.VZMemoryBalloonDeviceConfiguration{
			balloonConfig.VZMemoryBalloonDeviceConfiguration,
		})
	}
}
