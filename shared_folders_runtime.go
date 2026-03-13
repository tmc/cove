package main

import (
	"fmt"
	"os"

	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
	vz "github.com/tmc/apple/virtualization"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

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
	if s.vm.ID == 0 {
		return 0, fmt.Errorf("vm not initialized")
	}
	if s.vmQueue.Handle() == 0 {
		return 0, fmt.Errorf("vm queue not initialized")
	}

	var (
		applied  int
		applyErr error
	)

	DispatchSync(uintptr(s.vmQueue.Handle()), func() {
		state := vz.VZVirtualMachineState(s.vm.State())
		if state != vz.VZVirtualMachineStateRunning && state != vz.VZVirtualMachineStatePaused {
			applyErr = fmt.Errorf("vm not running (state=%s)", state.String())
			return
		}

		devices := s.vm.DirectorySharingDevices()
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
			applyErr = fmt.Errorf("shared folders device not found (restart VM with full profile)")
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
