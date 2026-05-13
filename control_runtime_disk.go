package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
	pvz "github.com/tmc/apple/private/virtualization"
	"github.com/tmc/apple/x/vzkit/storagehotplug"
	vmruntime "github.com/tmc/apple/x/vzkit/vm"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

// RuntimeDiskInfo describes one live storage device attached to a VM.
type RuntimeDiskInfo struct {
	Index         int    `json:"index"`
	Kind          string `json:"kind,omitempty"`
	Path          string `json:"path,omitempty"`
	ReadOnly      bool   `json:"read_only,omitempty"`
	FileSizeBytes uint64 `json:"file_size_bytes,omitempty"`
	ModTime       string `json:"mod_time,omitempty"`
	Description   string `json:"description,omitempty"`
}

// RuntimeDiskActionRequest is the flat JSON request shape for runtime disk
// commands. It accepts both snake_case and camelCase aliases for convenience.
type RuntimeDiskActionRequest struct {
	Action       string   `json:"action,omitempty"`
	Type         string   `json:"type,omitempty"`
	Index        *int     `json:"index,omitempty"`
	StorageIndex *int     `json:"storage_index,omitempty"`
	Path         string   `json:"path,omitempty"`
	PathAlt      string   `json:"file_path,omitempty"`
	ReadOnly     *bool    `json:"read_only,omitempty"`
	ReadOnlyAlt  *bool    `json:"readOnly,omitempty"`
	SizeBytes    *uint64  `json:"size_bytes,omitempty"`
	SizeBytesAlt *uint64  `json:"sizeBytes,omitempty"`
	SizeMB       *uint64  `json:"size_mb,omitempty"`
	SizeMBAlt    *uint64  `json:"sizeMB,omitempty"`
	SizeGB       *float64 `json:"size_gb,omitempty"`
	SizeGBAlt    *float64 `json:"sizeGB,omitempty"`
}

// RuntimeDiskListResponse is returned by list commands.
type RuntimeDiskListResponse struct {
	Action string            `json:"action"`
	Count  int               `json:"count"`
	Disks  []RuntimeDiskInfo `json:"disks"`
}

// RuntimeDiskMutationResponse is returned by swap/resize commands.
type RuntimeDiskMutationResponse struct {
	Action  string          `json:"action"`
	Index   int             `json:"index"`
	Disk    RuntimeDiskInfo `json:"disk"`
	Message string          `json:"message"`
}

type runtimeDiskEntry struct {
	Index  int
	Device pvz.VZStorageDevice
	Info   RuntimeDiskInfo
}

type runtimeDiskJSONEnvelope struct {
	Data json.RawMessage `json:"data,omitempty"`
}

func (r RuntimeDiskActionRequest) actionName() string {
	action := strings.TrimSpace(r.Action)
	if action == "" {
		action = strings.TrimSpace(r.Type)
	}
	return strings.ToLower(action)
}

func (r RuntimeDiskActionRequest) targetIndex() (int, bool) {
	if r.Index != nil {
		return *r.Index, true
	}
	if r.StorageIndex != nil {
		return *r.StorageIndex, true
	}
	return 0, false
}

func (r RuntimeDiskActionRequest) targetPath() string {
	if p := strings.TrimSpace(r.Path); p != "" {
		return p
	}
	return strings.TrimSpace(r.PathAlt)
}

func (r RuntimeDiskActionRequest) readOnlyValue() bool {
	if r.ReadOnly != nil {
		return *r.ReadOnly
	}
	if r.ReadOnlyAlt != nil {
		return *r.ReadOnlyAlt
	}
	return false
}

func (r RuntimeDiskActionRequest) requestedSizeBytes() (uint64, error) {
	var (
		size uint64
		set  bool
	)
	use := func(v uint64) error {
		if set {
			return fmt.Errorf("specify only one of size_bytes, size_mb, or size_gb")
		}
		size = v
		set = true
		return nil
	}
	if r.SizeBytes != nil && *r.SizeBytes > 0 {
		if err := use(*r.SizeBytes); err != nil {
			return 0, err
		}
	}
	if r.SizeBytesAlt != nil && *r.SizeBytesAlt > 0 {
		if err := use(*r.SizeBytesAlt); err != nil {
			return 0, err
		}
	}
	if r.SizeMB != nil && *r.SizeMB > 0 {
		if err := use(*r.SizeMB * 1024 * 1024); err != nil {
			return 0, err
		}
	}
	if r.SizeMBAlt != nil && *r.SizeMBAlt > 0 {
		if err := use(*r.SizeMBAlt * 1024 * 1024); err != nil {
			return 0, err
		}
	}
	if r.SizeGB != nil && *r.SizeGB > 0 {
		bytes := uint64(math.Round(*r.SizeGB * 1024 * 1024 * 1024))
		if err := use(bytes); err != nil {
			return 0, err
		}
	}
	if r.SizeGBAlt != nil && *r.SizeGBAlt > 0 {
		bytes := uint64(math.Round(*r.SizeGBAlt * 1024 * 1024 * 1024))
		if err := use(bytes); err != nil {
			return 0, err
		}
	}
	if !set {
		return 0, fmt.Errorf("size required")
	}
	return size, nil
}

func parseRuntimeDiskActionRequest(rawJSON []byte) (RuntimeDiskActionRequest, error) {
	if len(strings.TrimSpace(string(rawJSON))) == 0 {
		return RuntimeDiskActionRequest{}, fmt.Errorf("empty request")
	}

	var env runtimeDiskJSONEnvelope
	if err := json.Unmarshal(rawJSON, &env); err == nil && len(env.Data) > 0 {
		rawJSON = env.Data
	}

	var req RuntimeDiskActionRequest
	if err := json.Unmarshal(rawJSON, &req); err != nil {
		return RuntimeDiskActionRequest{}, fmt.Errorf("parse runtime disk request: %w", err)
	}
	return req, nil
}

func (s *ControlServer) handleDiskJSONRequest(rawJSON []byte) *controlpb.ControlResponse {
	req, err := parseRuntimeDiskActionRequest(rawJSON)
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}

	switch req.actionName() {
	case "list", "inspect":
		return s.handleDiskList()
	case "swap":
		if _, ok := req.targetIndex(); !ok {
			return &controlpb.ControlResponse{Error: "disk swap requires index"}
		}
		if strings.TrimSpace(req.targetPath()) == "" {
			return &controlpb.ControlResponse{Error: "disk swap requires path"}
		}
		return s.handleDiskSwap(req)
	case "resize":
		if _, ok := req.targetIndex(); !ok {
			return &controlpb.ControlResponse{Error: "disk resize requires index"}
		}
		return s.handleDiskResize(req)
	default:
		return &controlpb.ControlResponse{Error: fmt.Sprintf("unknown disk action: %s (use list, swap, or resize)", req.actionName())}
	}
}

func (s *ControlServer) handleDiskList() *controlpb.ControlResponse {
	entries, err := s.runtimeDiskEntries()
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	disks := make([]RuntimeDiskInfo, 0, len(entries))
	for _, entry := range entries {
		disks = append(disks, entry.Info)
	}
	return statusControlResponse(RuntimeDiskListResponse{
		Action: "list",
		Count:  len(disks),
		Disks:  disks,
	})
}

func (s *ControlServer) handleDiskSwap(req RuntimeDiskActionRequest) *controlpb.ControlResponse {
	idx, ok := req.targetIndex()
	if !ok {
		return &controlpb.ControlResponse{Error: "disk swap requires index"}
	}
	entry, err := s.runtimeDiskEntryByIndex(idx)
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}

	path := req.targetPath()
	attachment, err := newRuntimeDiskImageAttachment(path, req.readOnlyValue())
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("create disk attachment: %v", err)}
	}

	ctx, cancel := s.timeoutContext(30 * time.Second)
	defer cancel()

	if err := storagehotplug.SetAttachment(ctx, vmruntime.WrapQueue(s.vmQueue), entry.Device, attachment.VZStorageDeviceAttachment); err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("swap disk attachment: %v", err)}
	}

	updated, err := s.runtimeDiskEntryByIndex(entry.Index)
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}

	msg := fmt.Sprintf("swapped disk %d", entry.Index)
	if updated.Info.Path != "" {
		msg = fmt.Sprintf("%s to %s", msg, updated.Info.Path)
	}
	return statusControlResponse(RuntimeDiskMutationResponse{
		Action:  "swap",
		Index:   entry.Index,
		Disk:    updated.Info,
		Message: msg,
	})
}

func (s *ControlServer) handleDiskResize(req RuntimeDiskActionRequest) *controlpb.ControlResponse {
	idx, ok := req.targetIndex()
	if !ok {
		return &controlpb.ControlResponse{Error: "disk resize requires index"}
	}
	entry, err := s.runtimeDiskEntryByIndex(idx)
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}

	sizeBytes, err := req.requestedSizeBytes()
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}

	attachment, ok := runtimeDiskDiskImageAttachment(entry.Device)
	if !ok {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("disk %d is not backed by a disk image", entry.Index)}
	}

	if err := storagehotplug.UpdateDiskSize(attachment, sizeBytes); err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("resize disk attachment: %v", err)}
	}

	updated, err := s.runtimeDiskEntryByIndex(entry.Index)
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}

	msg := fmt.Sprintf("resized disk %d to %d bytes", entry.Index, sizeBytes)
	return statusControlResponse(RuntimeDiskMutationResponse{
		Action:  "resize",
		Index:   entry.Index,
		Disk:    updated.Info,
		Message: msg,
	})
}

func (s *ControlServer) runtimeDiskEntries() ([]runtimeDiskEntry, error) {
	if s.vm.ID == 0 || s.vmQueue.Handle() == 0 {
		return nil, fmt.Errorf("vm not configured")
	}

	var entries []runtimeDiskEntry

	DispatchSync(uintptr(s.vmQueue.Handle()), func() {
		machine := pvz.VZVirtualMachineFromID(s.vm.ID)
		devices := objc.Send[[]objc.ID](machine.ID, objc.Sel("_storageDevices"))
		if devices == nil {
			entries = nil
			return
		}

		count := len(devices)
		entries = make([]runtimeDiskEntry, 0, count)
		for i, id := range devices {
			if id == 0 {
				continue
			}
			device := pvz.VZStorageDeviceFromID(id)
			entries = append(entries, runtimeDiskEntry{
				Index:  i,
				Device: device,
				Info:   runtimeDiskInfoForDevice(i, device),
			})
		}
	})

	return entries, nil
}

func (s *ControlServer) runtimeDiskEntryByIndex(index int) (runtimeDiskEntry, error) {
	entries, err := s.runtimeDiskEntries()
	if err != nil {
		return runtimeDiskEntry{}, err
	}
	for _, entry := range entries {
		if entry.Index == index {
			return entry, nil
		}
	}
	return runtimeDiskEntry{}, fmt.Errorf("disk %d not found", index)
}

func runtimeDiskInfoForDevice(index int, device pvz.VZStorageDevice) RuntimeDiskInfo {
	info := RuntimeDiskInfo{
		Index:       index,
		Kind:        "storage-device",
		Description: strings.TrimSpace(device.Description()),
	}

	attachment, err := device.Attachment()
	if err != nil {
		return info
	}
	if attachment == nil || attachment.GetID() == 0 {
		return info
	}

	if diskAttachment, ok := runtimeDiskDiskImageAttachmentFromObject(attachment); ok {
		info.Kind = "disk-image"
		info.ReadOnly = diskAttachment.ReadOnly()
		if urlID := objc.Send[objc.ID](diskAttachment.ID, objc.Sel("URL")); urlID != 0 {
			if path := strings.TrimSpace(foundation.NSURLFromID(urlID).Path()); path != "" {
				info.Path = path
				if stat, err := os.Stat(path); err == nil {
					info.FileSizeBytes = uint64(stat.Size())
					info.ModTime = stat.ModTime().UTC().Format(time.RFC3339)
				}
			}
		}
		return info
	}

	info.Description = strings.TrimSpace(device.Description())
	return info
}

func runtimeDiskDiskImageAttachmentFromObject(obj objectivec.IObject) (pvz.VZDiskImageStorageDeviceAttachment, bool) {
	if obj == nil || obj.GetID() == 0 {
		return pvz.VZDiskImageStorageDeviceAttachment{}, false
	}
	if !objc.Send[bool](obj.GetID(), objc.Sel("respondsToSelector:"), objc.Sel("readOnly")) {
		return pvz.VZDiskImageStorageDeviceAttachment{}, false
	}
	if !objc.Send[bool](obj.GetID(), objc.Sel("respondsToSelector:"), objc.Sel("URL")) {
		return pvz.VZDiskImageStorageDeviceAttachment{}, false
	}
	return pvz.VZDiskImageStorageDeviceAttachmentFromID(obj.GetID()), true
}

func runtimeDiskDiskImageAttachment(device pvz.VZStorageDevice) (pvz.VZDiskImageStorageDeviceAttachment, bool) {
	attachment, err := device.Attachment()
	if err != nil {
		return pvz.VZDiskImageStorageDeviceAttachment{}, false
	}
	return runtimeDiskDiskImageAttachmentFromObject(attachment)
}
