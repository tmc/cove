package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/tmc/apple/dispatch"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
	vz "github.com/tmc/apple/virtualization"

	"github.com/tmc/vz-macos/internal/controlclient"
	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

type sharedFolderRuntimeApplier struct {
	vm    vz.VZVirtualMachine
	queue dispatch.Queue
}

type sharedFoldersRuntimeStatus = controlclient.SharedFoldersRuntimeStatus

func sharedFoldersDeviceMissingMessage() string {
	return "shared folders device not found (restart VM with the current cove runtime, using cove run -no-resume if needed, to pick up the shared-folders VirtioFS device)"
}

func newSharedFolderRuntimeApplier(vm vz.VZVirtualMachine, queue dispatch.Queue) sharedFolderRuntimeApplier {
	return sharedFolderRuntimeApplier{vm: vm, queue: queue}
}

func (s *ControlServer) handleSharedFoldersRuntimeStatus() *controlpb.ControlResponse {
	status := s.sharedFoldersRuntimeStatus()
	data, _ := json.Marshal(status)
	return &controlpb.ControlResponse{
		Success: true,
		Data:    string(data),
		Result:  &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: status.Message}},
	}
}

func (s *ControlServer) sharedFoldersRuntimeStatus() sharedFoldersRuntimeStatus {
	s.mu.Lock()
	applier := newSharedFolderRuntimeApplier(s.vm, s.vmQueue)
	s.mu.Unlock()
	return applier.Status()
}

func (s *ControlServer) handleSharedFoldersApply() *controlpb.ControlResponse {
	folders := LoadSharedFolders(s.effectiveVMDir())
	applied, err := s.applySharedFoldersToRunningVM(folders)
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	msg := fmt.Sprintf("applied %d shared folder(s)", applied)
	return &controlpb.ControlResponse{
		Success: true,
		Data:    msg,
		Result:  &controlpb.ControlResponse_Message{Message: &controlpb.MessageResponse{Message: msg}},
	}
}

func (s *ControlServer) applySharedFoldersToRunningVM(folders []SharedFolderEntry) (int, error) {
	s.mu.Lock()
	applier := newSharedFolderRuntimeApplier(s.vm, s.vmQueue)
	s.mu.Unlock()
	return applier.Apply(folders)
}

func (a sharedFolderRuntimeApplier) Apply(folders []SharedFolderEntry) (int, error) {
	if a.vm.ID == 0 {
		return 0, fmt.Errorf("vm not initialized")
	}
	if a.queue.Handle() == 0 {
		return 0, fmt.Errorf("vm queue not initialized")
	}

	var (
		applied  int
		applyErr error
	)

	DispatchSync(uintptr(a.queue.Handle()), func() {
		state := vz.VZVirtualMachineState(a.vm.State())
		if state != vz.VZVirtualMachineStateRunning && state != vz.VZVirtualMachineStatePaused {
			applyErr = fmt.Errorf("vm not running (state=%s)", vmStateLabel(state))
			return
		}

		devices := a.vm.DirectorySharingDevices()
		if len(devices) == 0 {
			applyErr = fmt.Errorf("no directory sharing devices configured")
			return
		}

		var device vz.VZVirtioFileSystemDevice
		for _, d := range devices {
			dev := vz.VZVirtioFileSystemDeviceFromID(d.ID)
			if dev.Tag() == SharedFoldersVirtioFSTag {
				device = dev
				break
			}
		}
		if device.ID == 0 {
			applyErr = fmt.Errorf("%s", sharedFoldersDeviceMissingMessage())
			return
		}

		if len(folders) == 0 {
			emptyDict := foundation.NewNSDictionary()
			emptyShare := vz.NewMultipleDirectoryShareWithDirectories(&emptyDict)
			device.SetShare(&emptyShare.VZDirectoryShare)
			applied = 0
			return
		}

		keys := make([]objectivec.IObject, 0, len(folders))
		values := make([]objectivec.IObject, 0, len(folders))
		for _, f := range folders {
			if _, err := os.Stat(f.Path); err != nil {
				continue
			}

			url := foundation.NewURLFileURLWithPath(f.Path)
			url.Retain()
			sharedDir := vz.NewSharedDirectoryWithURLReadOnly(url, f.ReadOnly)
			objc.Send[objc.ID](sharedDir.ID, objc.Sel("retain"))

			nsKey := objc.String(f.Tag)
			keys = append(keys, objectivec.ObjectFromID(nsKey))
			values = append(values, objectivec.ObjectFromID(sharedDir.ID))
		}

		if len(keys) == 0 {
			applyErr = fmt.Errorf("no existing shared folders to apply")
			return
		}

		dict := newDictFromSlices(values, keys)
		share := vz.NewMultipleDirectoryShareWithDirectories(&dict)
		device.SetShare(&share.VZDirectoryShare)
		applied = len(keys)
	})

	if applyErr != nil {
		return 0, applyErr
	}
	return applied, nil
}

func (a sharedFolderRuntimeApplier) Status() sharedFoldersRuntimeStatus {
	if a.vm.ID == 0 {
		return sharedFoldersRuntimeStatus{Message: "no live VM connected"}
	}
	if a.queue.Handle() == 0 {
		return sharedFoldersRuntimeStatus{Message: "vm queue not initialized"}
	}

	status := sharedFoldersRuntimeStatus{}
	DispatchSync(uintptr(a.queue.Handle()), func() {
		state := vz.VZVirtualMachineState(a.vm.State())
		status.State = vmStateLabel(state)
		status.Running = state == vz.VZVirtualMachineStateRunning || state == vz.VZVirtualMachineStatePaused
		if !status.Running {
			status.Message = fmt.Sprintf("vm not running (state=%s)", status.State)
			return
		}

		devices := a.vm.DirectorySharingDevices()
		if len(devices) == 0 {
			status.Message = "no directory sharing devices configured"
			return
		}
		for _, d := range devices {
			dev := vz.VZVirtioFileSystemDeviceFromID(d.ID)
			if dev.Tag() == SharedFoldersVirtioFSTag {
				status.VirtioFS = true
				status.Message = "shared folders VirtioFS device present"
				return
			}
		}
		status.Message = sharedFoldersDeviceMissingMessage()
	})
	return status
}
