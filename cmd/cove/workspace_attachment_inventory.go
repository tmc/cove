package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func verifyWorkspaceAttachmentPath(dir, path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("workspace attachment path is ambiguous")
	}
	relative, err := filepath.Rel(dir, path)
	if err != nil || relative == ".." || len(relative) > 3 && relative[:3] == "../" {
		return fmt.Errorf("external workspace attachment prevents automatic discard")
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil || canonical != path {
		return fmt.Errorf("workspace attachment identity is ambiguous")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("workspace attachment is not a regular local file")
	}
	return nil
}

func verifyWorkspaceAttachmentInventory(dir string, disks RuntimeDiskListResponse, usb runtimeUSBResponse) error {
	if disks.Count != len(disks.Disks) || disks.Count < 1 || disks.Count > 64 {
		return fmt.Errorf("workspace runtime disk inventory incomplete")
	}
	seen := map[int]bool{}
	for _, disk := range disks.Disks {
		if disk.Index < 0 || seen[disk.Index] || disk.Kind != "disk-image" {
			return fmt.Errorf("workspace runtime disk attachment is ambiguous")
		}
		seen[disk.Index] = true
		if err := verifyWorkspaceAttachmentPath(dir, disk.Path); err != nil {
			return err
		}
	}
	if !seen[0] {
		return fmt.Errorf("workspace primary runtime disk unavailable")
	}
	if !usb.OK || usb.List == nil {
		return fmt.Errorf("workspace USB attachment inventory unavailable")
	}
	for _, controller := range usb.List.Controllers {
		if controller.DeviceCount != len(controller.Devices) {
			return fmt.Errorf("workspace USB device inventory incomplete")
		}
		for _, device := range controller.Devices {
			switch device.Kind {
			case "VZUSBKeyboard", "VZUSBScreenCoordinatePointingDevice":
			default:
				return fmt.Errorf("workspace USB attachment prevents automatic discard")
			}
		}
	}
	return nil
}

func inspectWorkspaceAttachments(dir string) error {
	sock := GetControlSocketPathForVM(dir)
	var disks RuntimeDiskListResponse
	var usb runtimeUSBResponse
	for _, request := range []struct {
		kind string
		dst  any
	}{{"disk", &disks}, {"usb", &usb}} {
		response, err := ctlSendJSON(sock, map[string]interface{}{"type": request.kind, "data": map[string]interface{}{"action": "list"}}, 2*time.Second)
		if err != nil {
			return err
		}
		if response == nil || !response.Success || len(response.Data) > 64<<10 {
			return fmt.Errorf("workspace attachment inventory unavailable or unbounded")
		}
		if err := json.Unmarshal([]byte(response.Data), request.dst); err != nil {
			return err
		}
	}
	return verifyWorkspaceAttachmentInventory(dir, disks, usb)
}
