package main

import (
	"fmt"
	"strings"

	"github.com/tmc/apple/foundation"
	vz "github.com/tmc/apple/virtualization"
	storagex "github.com/tmc/apple/x/vzkit/storage"
)

// diskCachingModeFlag holds the -disk-caching flag value. When empty
// the framework default (derived from the storage cache policy) is
// kept unchanged.
var diskCachingModeFlag string

// diskImageCachingOverride parses -disk-caching. ok is false when the
// flag is unset, in which case callers keep existing behavior.
func diskImageCachingOverride() (mode vz.VZDiskImageCachingMode, ok bool, err error) {
	switch strings.ToLower(strings.TrimSpace(diskCachingModeFlag)) {
	case "":
		return 0, false, nil
	case "auto", "automatic":
		return vz.VZDiskImageCachingModeAutomatic, true, nil
	case "cached":
		return vz.VZDiskImageCachingModeCached, true, nil
	case "uncached":
		return vz.VZDiskImageCachingModeUncached, true, nil
	default:
		return 0, false, fmt.Errorf("invalid -disk-caching %q (must be auto, cached, or uncached)", diskCachingModeFlag)
	}
}

// createSystemDiskAttachmentWithCaching is createSystemDiskAttachment
// with the -disk-caching override applied. When the flag is unset it
// delegates to createSystemDiskAttachment so default behavior is
// unchanged.
func createSystemDiskAttachmentWithCaching(path string, readOnly bool) (vz.VZStorageDeviceAttachment, error) {
	caching, ok, err := diskImageCachingOverride()
	if err != nil {
		return vz.VZStorageDeviceAttachment{}, err
	}
	if !ok || runtimeSystemDiskAttachment != systemDiskAttachmentDiskImage {
		return createSystemDiskAttachment(path, readOnly)
	}

	path = strings.TrimSpace(effectiveSystemDiskPath(path))
	if path == "" {
		return vz.VZStorageDeviceAttachment{}, fmt.Errorf("disk path is required")
	}
	url := foundation.NewURLFileURLWithPath(path)
	if url.ID == 0 {
		return vz.VZStorageDeviceAttachment{}, fmt.Errorf("create file url")
	}
	url.Retain()

	policy := storagex.CacheDurable
	if readOnly {
		policy = storagex.CacheReadOnly
	}
	sync, err := diskImageSyncMode(policy)
	if err != nil {
		return vz.VZStorageDeviceAttachment{}, err
	}
	attachment, err := vz.NewDiskImageStorageDeviceAttachmentWithURLReadOnlyCachingModeSynchronizationModeError(url, readOnly, caching, sync)
	if err != nil {
		return vz.VZStorageDeviceAttachment{}, fmt.Errorf("create disk image attachment: %w", err)
	}
	attachment.Retain()
	base := vz.VZStorageDeviceAttachmentFromID(attachment.ID)
	base.Retain()
	return base, nil
}
