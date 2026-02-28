package diskimages2

import di2 "github.com/tmc/apple/private/diskimages2"

// AttachOptions configures disk image attachment behavior.
type AttachOptions struct {
	ReadOnly  bool
	AutoMount bool
	FileMode  int64 // 0 = default
}

// DeviceHandle represents an attached disk image device.
// Call [DeviceHandle.Release] to free the underlying ObjC handle.
type DeviceHandle struct {
	BSDName    string // e.g., "disk27"
	RegEntryID uint64
	handle     di2.DIDeviceHandle
}

// Release releases the underlying ObjC handle.
func (h *DeviceHandle) Release() {
	if h != nil && h.handle.ID != 0 {
		h.handle.Release()
		h.handle = di2.DIDeviceHandle{}
	}
}

// AttachedDeviceInfo describes a currently attached disk image.
type AttachedDeviceInfo struct {
	BSDName   string
	ImageURL  string
	MediaSize int64
}

// ImageInfo contains metadata about a disk image file.
type ImageInfo struct {
	// Raw is the NSDictionary returned by retrieveInfoWithParams:,
	// exposed as a map for callers that need framework-specific keys.
	Raw map[string]string
}
