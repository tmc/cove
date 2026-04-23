package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/tmc/apple/foundation"
	pvz "github.com/tmc/apple/private/virtualization"
	vz "github.com/tmc/apple/virtualization"
)

type systemDiskAttachmentMode int

const (
	systemDiskAttachmentDiskImage systemDiskAttachmentMode = iota
	systemDiskAttachmentTemporaryRAM
)

var (
	disposableSourceDiskPath      string
	runtimeSystemDiskPathOverride string
	runtimeSystemDiskAttachment   systemDiskAttachmentMode
)

func (m systemDiskAttachmentMode) String() string {
	switch m {
	case systemDiskAttachmentTemporaryRAM:
		return "temporary-ram"
	default:
		return "disk-image"
	}
}

func vmPrimaryDiskPath(vmPath string) string {
	if detectOSType(vmPath) == "Linux" {
		return filepath.Join(vmPath, "linux-disk.img")
	}
	return filepath.Join(vmPath, "disk.img")
}

func selectedVMSourceName() string {
	source := strings.TrimSpace(vmName)
	if source != "" {
		return source
	}
	return filepathBase(vmDir)
}

func effectiveSystemDiskPath(path string) string {
	if override := strings.TrimSpace(runtimeSystemDiskPathOverride); override != "" {
		return override
	}
	return path
}

func createRuntimeStorageDeviceAttachment(path string, readOnly bool, mode systemDiskAttachmentMode) (pvz.VZStorageDeviceAttachment, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return pvz.VZStorageDeviceAttachment{}, fmt.Errorf("disk path is required")
	}

	url := foundation.NewURLFileURLWithPath(path)
	if url.ID == 0 {
		return pvz.VZStorageDeviceAttachment{}, fmt.Errorf("create file url")
	}
	url.Retain()

	if mode == systemDiskAttachmentTemporaryRAM {
		attachment, err := pvz.NewVZTemporaryRAMStorageDeviceAttachmentWithURLReadOnlyError(url, readOnly)
		if err != nil {
			return pvz.VZStorageDeviceAttachment{}, fmt.Errorf("create temporary ram attachment: %w", err)
		}
		attachment.Retain()
		return attachment.VZStorageDeviceAttachment, nil
	}

	attachment, err := vz.NewDiskImageStorageDeviceAttachmentWithURLReadOnlyError(url, readOnly)
	if err != nil {
		return pvz.VZStorageDeviceAttachment{}, fmt.Errorf("create disk image attachment: %w", err)
	}
	attachment.Retain()
	return pvz.VZStorageDeviceAttachmentFromID(attachment.ID), nil
}

func createSystemDiskAttachment(path string, readOnly bool) (vz.VZStorageDeviceAttachment, error) {
	attachment, err := createRuntimeStorageDeviceAttachment(effectiveSystemDiskPath(path), readOnly, runtimeSystemDiskAttachment)
	if err != nil {
		return vz.VZStorageDeviceAttachment{}, err
	}
	base := vz.VZStorageDeviceAttachmentFromID(attachment.ID)
	base.Retain()
	return base, nil
}

func runDisposableCloneFromDiskPath(source, diskPath string, attachmentMode systemDiskAttachmentMode) error {
	if disposableMode {
		return fmt.Errorf("this command already creates a disposable clone; do not combine it with -disposable")
	}
	if rollbackSnapshotName != "" {
		return fmt.Errorf("rollback snapshot runs already create a disposable clone")
	}
	if strings.TrimSpace(source) == "" {
		return fmt.Errorf("source vm is required")
	}
	if strings.TrimSpace(diskPath) == "" {
		return fmt.Errorf("source disk path is required")
	}

	prevDisposableMode := disposableMode
	prevDisposableSourceDiskPath := disposableSourceDiskPath
	prevDiskPathOverride := runtimeSystemDiskPathOverride
	prevAttachmentMode := runtimeSystemDiskAttachment
	prevLinuxMode := linuxMode
	disposableMode = true
	disposableSourceDiskPath = diskPath
	runtimeSystemDiskPathOverride = ""
	runtimeSystemDiskAttachment = attachmentMode
	linuxMode = detectOSType(GetVMPath(source)) == "Linux"
	defer func() {
		disposableMode = prevDisposableMode
		disposableSourceDiskPath = prevDisposableSourceDiskPath
		runtimeSystemDiskPathOverride = prevDiskPathOverride
		runtimeSystemDiskAttachment = prevAttachmentMode
		linuxMode = prevLinuxMode
	}()

	return runCurrentVM()
}
