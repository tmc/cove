package main

import (
	"fmt"
	"strings"

	"github.com/tmc/apple/foundation"
	pvz "github.com/tmc/apple/private/virtualization"
	vz "github.com/tmc/apple/virtualization"
)

type DiskCachePolicy int

const (
	DiskCacheDurable DiskCachePolicy = iota
	DiskCacheEphemeral
	DiskCacheReadOnly
)

func diskAttachmentModes(policy DiskCachePolicy) (vz.VZDiskImageCachingMode, vz.VZDiskImageSynchronizationMode, error) {
	switch policy {
	case DiskCacheDurable:
		return vz.VZDiskImageCachingModeCached, vz.VZDiskImageSynchronizationModeFsync, nil
	case DiskCacheEphemeral:
		return vz.VZDiskImageCachingModeCached, vz.VZDiskImageSynchronizationModeNone, nil
	case DiskCacheReadOnly:
		return vz.VZDiskImageCachingModeAutomatic, vz.VZDiskImageSynchronizationModeFull, nil
	default:
		return 0, 0, fmt.Errorf("unknown disk cache policy %d", policy)
	}
}

func overrideDiskSyncMode(sync vz.VZDiskImageSynchronizationMode) (vz.VZDiskImageSynchronizationMode, error) {
	switch strings.ToLower(strings.TrimSpace(diskSyncMode)) {
	case "":
		return sync, nil
	case "fsync":
		return vz.VZDiskImageSynchronizationModeFsync, nil
	case "none":
		return vz.VZDiskImageSynchronizationModeNone, nil
	case "full":
		return vz.VZDiskImageSynchronizationModeFull, nil
	default:
		return 0, fmt.Errorf("invalid -disk-sync %q (must be fsync, none, or full)", diskSyncMode)
	}
}

func newDiskAttachment(url foundation.INSURL, readOnly bool, policy DiskCachePolicy) (vz.VZDiskImageStorageDeviceAttachment, error) {
	caching, sync, err := diskAttachmentModes(policy)
	if err != nil {
		return vz.VZDiskImageStorageDeviceAttachment{}, err
	}
	sync, err = overrideDiskSyncMode(sync)
	if err != nil {
		return vz.VZDiskImageStorageDeviceAttachment{}, err
	}
	return vz.NewDiskImageStorageDeviceAttachmentWithURLReadOnlyCachingModeSynchronizationModeError(url, readOnly, caching, sync)
}

func newRuntimeDiskImageAttachment(path string, readOnly bool) (pvz.VZDiskImageStorageDeviceAttachment, error) {
	url := foundation.NewURLFileURLWithPath(path)
	if url.ID == 0 {
		return pvz.VZDiskImageStorageDeviceAttachment{}, fmt.Errorf("create file url")
	}
	attachment, err := newDiskAttachment(url, readOnly, DiskCacheDurable)
	if err != nil {
		return pvz.VZDiskImageStorageDeviceAttachment{}, fmt.Errorf("create disk image attachment: %w", err)
	}
	attachment.Retain()
	return pvz.VZDiskImageStorageDeviceAttachmentFromID(attachment.ID), nil
}
