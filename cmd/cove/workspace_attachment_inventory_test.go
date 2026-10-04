package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceAttachmentInventoryRefusesExternalAndAmbiguous(t *testing.T) {
	dir := resolvePath(t.TempDir())
	local := filepath.Join(dir, "disk.img")
	os.WriteFile(local, nil, 0600)
	external := filepath.Join(t.TempDir(), "data.img")
	os.WriteFile(external, nil, 0600)
	usb := runtimeUSBResponse{OK: true, List: &runtimeUSBListResponse{}}
	for _, tt := range []struct {
		name       string
		path, kind string
		want       bool
	}{
		{"local", local, "disk-image", true}, {"external-attached", external, "disk-image", false}, {"unknown-attachment", local, "storage-device", false}, {"missing-path", "", "disk-image", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			disks := RuntimeDiskListResponse{Count: 1, Disks: []RuntimeDiskInfo{{Index: 0, Kind: tt.kind, Path: tt.path}}}
			err := verifyWorkspaceAttachmentInventory(dir, disks, usb)
			if (err == nil) != tt.want {
				t.Fatalf("inventory error=%v", err)
			}
		})
	}
	disks := RuntimeDiskListResponse{Count: 2, Disks: []RuntimeDiskInfo{{Index: 0, Kind: "disk-image", Path: local}, {Index: 1, Kind: "disk-image", Path: external}}}
	if verifyWorkspaceAttachmentInventory(dir, disks, usb) == nil {
		t.Fatal("accepted additional external attachment")
	}
	usb.List.Controllers = []runtimeUSBControllerInfo{{DeviceCount: 1, Devices: []runtimeUSBDeviceInfo{{Kind: "VZUSBMassStorageDevice", Path: external}}}}
	disks.Disks = disks.Disks[:1]
	disks.Count = 1
	if verifyWorkspaceAttachmentInventory(dir, disks, usb) == nil {
		t.Fatal("accepted external USB storage")
	}
	linked := filepath.Join(dir, "aux.img")
	os.Symlink(external, linked)
	if verifyWorkspaceAttachmentPath(dir, linked) == nil {
		t.Fatal("accepted external auxiliary symlink")
	}
}
